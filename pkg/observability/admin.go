package observability

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

// adminServer serves the endpoints the platform talks to, on a port of their
// own: /metrics for Prometheus, /healthz and /readyz for the kubelet.
//
// It is deliberately not on the service's own port: scraping and probing should
// not queue behind traffic, and this port exposes internals, so it must never be
// routed to from outside the cluster.
type adminServer struct {
	http    *http.Server
	lis     net.Listener
	log     *zap.Logger
	timeout time.Duration
	mu      sync.RWMutex
	checks  []readinessCheck
}

type readinessCheck struct {
	name  string
	check func(ctx context.Context) error
}

func startAdmin(cfg Config, reg *prometheus.Registry, log *zap.Logger) (*adminServer, error) {
	lis, err := net.Listen("tcp", cfg.AdminAddr)
	if err != nil {
		return nil, fmt.Errorf("observability: listen on %s: %w", cfg.AdminAddr, err)
	}

	s := &adminServer{lis: lis, log: log, timeout: cfg.ReadinessTimeout}

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		// A scrape failing on one broken collector loses every other metric in
		// the process along with it, at exactly the moment they are wanted.
		ErrorHandling: promhttp.ContinueOnError,
		ErrorLog:      zap.NewStdLog(log),
	}))
	mux.HandleFunc("GET /healthz", s.handleLive)
	mux.HandleFunc("GET /readyz", s.handleReady)

	s.http = &http.Server{
		Handler: mux,
		// A probe or a scrape that cannot finish in this long has already
		// failed as far as its caller is concerned.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		if err := s.http.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("admin server stopped", zap.Error(err))
		}
	}()

	return s, nil
}

func (s *adminServer) addr() string {
	return s.lis.Addr().String()
}

func (s *adminServer) addReadinessCheck(name string, check func(ctx context.Context) error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.checks = append(s.checks, readinessCheck{name: name, check: check})
}

func (s *adminServer) shutdown(ctx context.Context) error {
	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("observability: shut down admin server: %w", err)
	}

	return nil
}

// handleLive answers whether the process is still running at all.
//
// It checks nothing. A liveness failure restarts the pod, and restarting because
// a database is unreachable turns one outage into a crash-loop across every
// replica. Dependencies belong in readiness, which takes the pod out of rotation
// and puts it back when they recover.
func (s *adminServer) handleLive(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

// handleReady runs every registered dependency check.
//
// Checks run in parallel under one shared budget, so readiness costs the
// slowest dependency rather than the sum of them all. The body names what
// failed, because a probe that only says "not ready" leaves whoever is paged
// checking each dependency by hand.
func (s *adminServer) handleReady(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	checks := make([]readinessCheck, len(s.checks))
	copy(checks, s.checks)
	s.mu.RUnlock()

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	results := make([]string, len(checks))
	failed := make([]bool, len(checks))

	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()

			if err := c.check(ctx); err != nil {
				results[i] = c.name + ": " + err.Error()
				failed[i] = true

				return
			}
			results[i] = c.name + ": ok"
		}()
	}
	wg.Wait()

	ready := true
	for _, f := range failed {
		if f {
			ready = false
			break
		}
	}

	// Sorted so a diff between two probes shows what changed rather than what
	// happened to finish first.
	sort.Strings(results)

	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
		s.log.Warn("readiness check failed", zap.Strings("checks", results))
	}

	body := "ok\n"
	if len(results) > 0 {
		body = strings.Join(results, "\n") + "\n"
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
