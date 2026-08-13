package observability_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/observability"
)

// testConfig goes through pkg/config rather than a struct literal: the
// behaviour under test lives in the `envDefault` tags, and a literal would
// silently disable tracing and give every timeout a zero value.
func testConfig(t *testing.T, overrides map[string]string) observability.Config {
	t.Helper()

	environ := map[string]string{
		"SERVICE": "grpcx-test",
		// Loopback with an ephemeral port: these tests run in parallel with
		// whatever else is on the machine, and :9090 is a popular address.
		"ADMIN_ADDR": "127.0.0.1:0",
		// Nothing is listening for spans in a test, and an exporter retrying
		// against a dead collector only adds noise.
		"TRACING_ENABLED": "false",
		// Record every span this process starts, so the assertions below do not
		// depend on a 10% sampling coin flip.
		"SAMPLE_RATIO": "1",
	}
	for k, v := range overrides {
		environ[k] = v
	}

	cfg, err := config.Load[observability.Config](config.WithEnviron(environ))
	if err != nil {
		t.Fatalf("load observability config: %v", err)
	}

	return cfg
}

func start(t *testing.T, overrides map[string]string) *observability.Provider {
	t.Helper()

	p, err := observability.Start(context.Background(), testConfig(t, overrides))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := p.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	return p
}

func get(t *testing.T, addr, path string) (int, string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		t.Fatalf("build request for %s: %v", path, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s body: %v", path, err)
	}

	return resp.StatusCode, string(body)
}

// The whole point of Start is that it runs before anything reads a global.
// otelgrpc resolves otel.GetTracerProvider when its handler is built, so a
// global still no-op after Start means every span in the process is silently
// discarded and nothing ever says so.
func TestStartInstallsGlobalProviders(t *testing.T) {
	start(t, nil)

	_, span := otel.GetTracerProvider().Tracer("test").Start(context.Background(), "span")
	defer span.End()

	// A no-op provider hands back a span that is valid-looking but records
	// nothing, so recording — not validity — is what distinguishes them.
	if !span.IsRecording() {
		t.Error("spans are not being recorded; the global provider is still the no-op one")
	}
	if !span.SpanContext().IsValid() {
		t.Error("the span context produced after Start is not valid")
	}
}

// Without W3C trace context on the propagator the trace ends at the first hop:
// no traceparent on gRPC metadata, and none in the outbox headers that carry it
// onto Kafka.
func TestStartInstallsTraceContextPropagator(t *testing.T) {
	start(t, nil)

	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
		SpanID:     trace.SpanID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		TraceFlags: trace.FlagsSampled,
	}))

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	if _, ok := carrier["traceparent"]; !ok {
		t.Errorf("no traceparent was injected; carrier = %v", carrier)
	}
}

// The metrics endpoint is the only way anything this process measures leaves
// it, runtime metrics included.
func TestMetricsEndpointServesRuntimeMetrics(t *testing.T) {
	p := start(t, nil)

	status, body := get(t, p.AdminAddr(), "/metrics")
	if status != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want %d", status, http.StatusOK)
	}
	for _, want := range []string{"go_goroutines", "process_start_time_seconds"} {
		if !strings.Contains(body, want) {
			t.Errorf("%s is missing from the scrape output", want)
		}
	}
}

// Liveness must not check dependencies. A failing liveness probe restarts the
// pod, and restarting because a database is unreachable turns one outage into a
// crash-loop across every replica.
func TestLivenessIgnoresFailingDependencies(t *testing.T) {
	p := start(t, nil)
	p.AddReadinessCheck("postgres", func(context.Context) error {
		return errors.New("connection refused")
	})

	if status, _ := get(t, p.AdminAddr(), "/healthz"); status != http.StatusOK {
		t.Errorf("GET /healthz = %d while a dependency was down, want %d", status, http.StatusOK)
	}
}

func TestReadinessReportsDependencies(t *testing.T) {
	p := start(t, nil)

	// Ready before anything is registered: checks are added as each dependency
	// is wired, so an empty set means nothing has claimed to be unready.
	if status, _ := get(t, p.AdminAddr(), "/readyz"); status != http.StatusOK {
		t.Errorf("GET /readyz with no checks = %d, want %d", status, http.StatusOK)
	}

	p.AddReadinessCheck("postgres", func(context.Context) error { return nil })
	if status, body := get(t, p.AdminAddr(), "/readyz"); status != http.StatusOK {
		t.Errorf("GET /readyz with a passing check = %d (%q), want %d", status, body, http.StatusOK)
	}

	p.AddReadinessCheck("kafka", func(context.Context) error {
		return errors.New("no brokers available")
	})

	status, body := get(t, p.AdminAddr(), "/readyz")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz with a failing check = %d, want %d", status, http.StatusServiceUnavailable)
	}
	// The body has to name what failed, or whoever is paged checks every
	// dependency by hand.
	if !strings.Contains(body, "kafka: no brokers available") {
		t.Errorf("the failing dependency is not named in the body: %q", body)
	}
	if !strings.Contains(body, "postgres: ok") {
		t.Errorf("the passing dependency is not reported in the body: %q", body)
	}
}

// Checks run in parallel under one shared budget, so readiness costs the
// slowest dependency rather than the sum of them all.
func TestReadinessBoundsSlowChecks(t *testing.T) {
	p := start(t, map[string]string{"READINESS_TIMEOUT": "100ms"})

	for _, name := range []string{"postgres", "kafka", "redis"} {
		p.AddReadinessCheck(name, func(ctx context.Context) error {
			<-ctx.Done()

			return ctx.Err()
		})
	}

	started := time.Now()
	status, _ := get(t, p.AdminAddr(), "/readyz")
	elapsed := time.Since(started)

	if status != http.StatusServiceUnavailable {
		t.Errorf("GET /readyz = %d, want %d", status, http.StatusServiceUnavailable)
	}
	// Serially the three checks would take 300ms; the budget is shared.
	if elapsed > 250*time.Millisecond {
		t.Errorf("readiness took %s for three parallel checks on a 100ms budget", elapsed)
	}
}

// Start binds the admin port itself so a clashing or malformed address fails
// while main is still wiring, rather than after everything else is up.
func TestStartFailsOnUnusableAdminAddress(t *testing.T) {
	cfg := testConfig(t, map[string]string{"ADMIN_ADDR": "127.0.0.1:not-a-port"})

	p, err := observability.Start(context.Background(), cfg)
	if err == nil {
		_ = p.Shutdown(context.Background())
		t.Fatal("Start succeeded on an unusable admin address")
	}
}
