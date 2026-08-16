package httpx

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
)

// latencyBuckets match the ones pkg/grpcx/server uses, so a BFF endpoint and the
// calls it fans out to can be read on the same axis.
var latencyBuckets = []float64{
	0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// unmatchedRoute is the route label for a request that matched no pattern.
//
// Without it every 404 would be labelled with the path it asked for, and a
// scanner walking URLs would create a time series per guess.
const unmatchedRoute = "unmatched"

// serverMetrics is the RED set — rate, errors, duration — per route, plus the
// counter that says something is broken rather than slow.
type serverMetrics struct {
	handled  *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight *prometheus.GaugeVec
	panics   *prometheus.CounterVec
}

// newServerMetrics builds and registers the collectors.
//
// Labelled by route pattern rather than by path, so /api/v1/users/{id} is one
// series instead of one per user. The duration histogram carries no status
// label for the reason its gRPC counterpart does not: rate and errors come off
// the counter, and thirteen buckets multiplied by every status a route can
// answer is how a metrics backend drowns.
func newServerMetrics(reg prometheus.Registerer) (*serverMetrics, error) {
	m := &serverMetrics{
		handled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_server_handled_total",
			Help: "Total HTTP requests completed, by status code.",
		}, []string{"method", "route", "code"}),

		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_server_handling_seconds",
			Help:    "Wall time taken to handle an HTTP request.",
			Buckets: latencyBuckets,
		}, []string{"method", "route"}),

		inFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "http_server_in_flight_requests",
			Help: "HTTP requests currently being handled.",
		}, []string{"method"}),

		panics: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_server_panics_recovered_total",
			Help: "Panics recovered by the HTTP recovery middleware.",
		}, []string{"method", "route"}),
	}

	for _, c := range []prometheus.Collector{m.handled, m.duration, m.inFlight, m.panics} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("httpx: register metrics: %w", err)
		}
	}

	return m, nil
}

// metricsMiddleware records one observation per completed request.
//
// It sits outside [Recover] so that a panicked request is measured as the 500
// the recoverer turned it into. Inside, the panic would unwind past this and the
// status captured would be whatever had not been written yet.
//
// In-flight is labelled by method alone: it is a gauge read during an incident,
// and the route breakdown that helps there is already on the histogram.
func metricsMiddleware(m *serverMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inFlight := m.inFlight.WithLabelValues(r.Method)
			inFlight.Inc()
			defer inFlight.Dec()

			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			start := time.Now()
			next.ServeHTTP(wrapped, r)
			elapsed := time.Since(start)

			route := routePattern(r)
			m.handled.WithLabelValues(r.Method, route, strconv.Itoa(statusOrOK(wrapped.Status()))).Inc()
			m.duration.WithLabelValues(r.Method, route).Observe(elapsed.Seconds())
		})
	}
}

// routePattern returns the chi pattern this request matched.
//
// It is only meaningful after the mux has routed, which is why every caller
// reads it once the inner handler has returned.
func routePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return unmatchedRoute
	}

	if pattern := rctx.RoutePattern(); pattern != "" {
		return pattern
	}

	return unmatchedRoute
}

// statusOrOK reads a captured status, treating "nothing written" as 200 — which
// is what net/http sends for a handler that returns without calling WriteHeader.
func statusOrOK(status int) int {
	if status == 0 {
		return http.StatusOK
	}

	return status
}
