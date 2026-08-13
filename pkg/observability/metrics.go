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
// Go and process metrics are here rather than left to whoever remembers,
// because the questions they answer — is this pod leaking goroutines, is it
// about to be OOM-killed, how much of the GC is in the p99 — are the first ones
// asked when a service goes slow and nothing in the business metrics explains it.
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
// This is the decision the package exists to make once: without it the global
// meter provider stays a no-op, and any instrumented library added later emits
// nothing while reporting no error. Routing it through the Prometheus exporter
// rather than OTLP keeps metrics on one path — scraped — instead of splitting
// them across two backends that then disagree.
//
// pkg/grpcx suppresses otelgrpc's own RPC metrics on top of this, because it
// already publishes the grpc_server_* set and two overlapping views of the same
// calls cost cardinality while making dashboards ambiguous.
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
