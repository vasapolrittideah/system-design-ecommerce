package observability

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

// newRegistry builds the process registry, pre-loaded with the runtime
// collectors.
//
// Go and process metrics are here rather than left to whoever remembers, because
// leaking goroutines, an imminent OOM-kill, and GC in the p99 are the first
// things asked about when nothing in the business metrics explains a slowdown.
func newRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return reg
}

// newMeterProvider points otel's metrics at the same Prometheus registry
// everything else in the process writes to.
//
// Without it the global meter provider stays a no-op and any instrumented
// library added later emits nothing, reporting no error. Going through the
// Prometheus exporter rather than OTLP keeps metrics on one path instead of
// splitting them across two backends that then disagree.
//
// pkg/grpcx suppresses otelgrpc's own RPC metrics on top of this, so the same
// calls are not measured twice under two naming schemes.
func newMeterProvider(res *resource.Resource, reg prometheus.Registerer) (*sdkmetric.MeterProvider, error) {
	exporter, err := otelprom.New(otelprom.WithRegisterer(reg))
	if err != nil {
		return nil, fmt.Errorf("observability: build Prometheus exporter: %w", err)
	}

	return sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(exporter),
	), nil
}
