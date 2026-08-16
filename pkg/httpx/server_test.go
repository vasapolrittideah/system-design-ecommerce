package httpx_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
)

func TestServerServesUntilContextIsCancelled(t *testing.T) {
	srv, err := httpx.NewServer(httpx.ServerConfig{
		Addr:              "127.0.0.1:0",
		ReadHeaderTimeout: time.Second,
		ShutdownTimeout:   5 * time.Second,
	}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx) }()

	res, err := http.Get("http://" + srv.Addr().String())
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()

	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}

	cancel()

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve() error = %v, want nil after a clean drain", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() did not return after the context was cancelled")
	}
}

// A second process binding the same port has to fail while main is still
// wiring, not once it is already serving nothing.
func TestNewServerReportsAnUnavailablePort(t *testing.T) {
	first, err := httpx.NewServer(httpx.ServerConfig{Addr: "127.0.0.1:0"}, http.NotFoundHandler())
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	if _, err := httpx.NewServer(httpx.ServerConfig{Addr: first.Addr().String()}, http.NotFoundHandler()); err == nil {
		t.Error("NewServer() on a bound port returned no error")
	}
}

// The budget reaches the handler as a context deadline, which is the only way
// pkg/grpcx/client learns about it — it inherits whatever the request carries.
func TestRequestTimeoutBecomesAContextDeadline(t *testing.T) {
	var (
		deadline time.Time
		ok       bool
	)

	r := httpx.MustNewRouter(httpx.MustNewValidator(), httpx.WithRequestTimeout(250*time.Millisecond))
	r.Get("/budget", func(w http.ResponseWriter, req *http.Request) {
		deadline, ok = req.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/budget", nil))

	if !ok {
		t.Fatal("handler context carries no deadline")
	}

	if remaining := time.Until(deadline); remaining <= 0 || remaining > 250*time.Millisecond {
		t.Errorf("deadline is %s away, want the configured budget", remaining)
	}
}

// A router built without a registry must still work: metrics are the one part
// of the chain that is optional, because a test cannot share a registry with
// the next test.
func TestRouterWithoutMetricsStillServes(t *testing.T) {
	r := httpx.MustNewRouter(httpx.MustNewValidator())
	r.Get("/ping", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/ping", nil))

	if res.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", res.Code, http.StatusOK)
	}
}

func TestMetricsLabelByRoutePatternNotPath(t *testing.T) {
	reg := prometheus.NewRegistry()

	r := httpx.MustNewRouter(httpx.MustNewValidator(), httpx.WithRouterMetrics(reg))
	r.Get("/users/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	for _, id := range []string{"alice", "bob", "carol"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/"+id, nil))
	}

	labels := routeLabels(t, reg, "http_server_handled_total")

	// One series, not one per user. Labelling by path is how a metrics backend
	// acquires a time series per row of a table.
	if len(labels) != 1 || labels[0] != "/users/{id}" {
		t.Errorf("route labels = %v, want exactly [/users/{id}]", labels)
	}
}

// An unrouted path is the case that would otherwise be labelled with whatever a
// scanner asked for.
func TestMetricsCollapseUnroutedPaths(t *testing.T) {
	reg := prometheus.NewRegistry()

	r := httpx.MustNewRouter(httpx.MustNewValidator(), httpx.WithRouterMetrics(reg))
	// chi runs no middleware at all on a mux with zero routes registered, so a
	// router that never matches anything is not the case worth measuring.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	for _, path := range []string{"/wp-admin", "/.env", "/phpmyadmin"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	labels := routeLabels(t, reg, "http_server_handled_total")

	if len(labels) != 1 || labels[0] != "unmatched" {
		t.Errorf("route labels = %v, want exactly [unmatched]", labels)
	}
}

// A panicked request is measured and logged as the 500 the recoverer produced.
// With recovery outside the observability middleware it would unwind past both
// and be counted as a success, or not counted at all.
func TestPanicIsCountedAndAnsweredAsFiveHundred(t *testing.T) {
	reg := prometheus.NewRegistry()

	r := httpx.MustNewRouter(httpx.MustNewValidator(), httpx.WithRouterMetrics(reg))
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })

	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusInternalServerError)
	}

	if strings.Contains(res.Body.String(), "boom") {
		t.Errorf("body leaks the panic value: %s", res.Body)
	}

	if got := counterValue(t, reg, "http_server_panics_recovered_total"); got != 1 {
		t.Errorf("http_server_panics_recovered_total = %v, want 1", got)
	}

	if got := statusLabels(t, reg, "http_server_handled_total"); len(got) != 1 || got[0] != "500" {
		t.Errorf("status labels = %v, want [500]", got)
	}
}

// http.ErrAbortHandler means a handler deliberately dropped the connection.
// Swallowing it would answer a request nobody is listening to.
func TestAbortHandlerIsNotRecovered(t *testing.T) {
	r := httpx.MustNewRouter(httpx.MustNewValidator())
	r.Get("/abort", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })

	defer func() {
		recovered := recover()
		if err, ok := recovered.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Errorf("recovered %v, want http.ErrAbortHandler to pass through", recovered)
		}
	}()

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abort", nil))
}

// The exemplar is what turns a point on a latency panel into the trace that
// produced it. Without it a slow p99 is a number with no way back to the
// request behind it.
func TestLatencyCarriesTraceExemplar(t *testing.T) {
	reg := prometheus.NewRegistry()

	r := httpx.MustNewRouter(httpx.MustNewValidator(), httpx.WithRouterMetrics(reg))
	r.Get("/orders", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/orders", nil)
	req = req.WithContext(trace.ContextWithSpanContext(req.Context(), spanContext(t, trace.FlagsSampled)))

	r.ServeHTTP(httptest.NewRecorder(), req)

	ids := exemplarTraceIDs(t, reg, "http_server_handling_seconds")
	if len(ids) == 0 {
		t.Fatal("no exemplar on the latency histogram — a dashboard has nothing to link from")
	}
	for _, id := range ids {
		if id != testTraceID {
			t.Errorf("exemplar trace_id = %q, want %q", id, testTraceID)
		}
	}
}

// An unsampled span must not leave an exemplar behind: its trace was never
// exported, so the link would lead to nothing in Jaeger.
func TestUnsampledRequestCarriesNoExemplar(t *testing.T) {
	reg := prometheus.NewRegistry()

	r := httpx.MustNewRouter(httpx.MustNewValidator(), httpx.WithRouterMetrics(reg))
	r.Get("/orders", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/orders", nil)
	req = req.WithContext(trace.ContextWithSpanContext(req.Context(), spanContext(t, 0)))

	r.ServeHTTP(httptest.NewRecorder(), req)

	if ids := exemplarTraceIDs(t, reg, "http_server_handling_seconds"); len(ids) != 0 {
		t.Errorf("exemplars = %v, want none for an unsampled span", ids)
	}
}

// The IDs from the W3C trace context specification's own example.
const (
	testTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	testSpanID  = "00f067aa0ba902b7"
)

func spanContext(t *testing.T, flags trace.TraceFlags) trace.SpanContext {
	t.Helper()

	traceID, err := trace.TraceIDFromHex(testTraceID)
	if err != nil {
		t.Fatalf("TraceIDFromHex() error = %v", err)
	}
	spanID, err := trace.SpanIDFromHex(testSpanID)
	if err != nil {
		t.Fatalf("SpanIDFromHex() error = %v", err)
	}

	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: flags,
	})
}

func exemplarTraceIDs(t *testing.T, reg prometheus.Gatherer, metric string) []string {
	t.Helper()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	var ids []string
	for _, family := range families {
		if family.GetName() != metric {
			continue
		}
		for _, m := range family.GetMetric() {
			for _, bucket := range m.GetHistogram().GetBucket() {
				for _, pair := range bucket.GetExemplar().GetLabel() {
					if pair.GetName() == "trace_id" {
						ids = append(ids, pair.GetValue())
					}
				}
			}
		}
	}

	return ids
}

func labelValues(t *testing.T, reg prometheus.Gatherer, metric, label string) []string {
	t.Helper()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	var values []string
	for _, family := range families {
		if family.GetName() != metric {
			continue
		}
		for _, m := range family.GetMetric() {
			for _, pair := range m.GetLabel() {
				if pair.GetName() == label {
					values = append(values, pair.GetValue())
				}
			}
		}
	}

	return values
}

func routeLabels(t *testing.T, reg prometheus.Gatherer, metric string) []string {
	t.Helper()

	return labelValues(t, reg, metric, "route")
}

func statusLabels(t *testing.T, reg prometheus.Gatherer, metric string) []string {
	t.Helper()

	return labelValues(t, reg, metric, "code")
}

func counterValue(t *testing.T, reg prometheus.Gatherer, metric string) float64 {
	t.Helper()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	var total float64
	for _, family := range families {
		if family.GetName() != metric {
			continue
		}
		for _, m := range family.GetMetric() {
			total += m.GetCounter().GetValue()
		}
	}

	return total
}
