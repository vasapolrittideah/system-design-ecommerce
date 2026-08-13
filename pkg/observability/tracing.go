package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// newTracerProvider builds the provider spans are recorded through.
//
// Export goes over OTLP/gRPC, which Jaeger and Tempo both accept, through a
// batch processor: spans are queued and flushed on a timer rather than sent
// inline, so a slow collector costs memory instead of request latency.
//
// With tracing disabled the provider still exists and still creates spans, it
// just never exports them. That is deliberate — a provider that is present but
// silent keeps context propagation intact, where a no-op provider would break
// the trace of every service downstream of this one.
func newTracerProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler(cfg)),
	}

	if cfg.TracingEnabled {
		exporter, err := newExporter(ctx, cfg)
		if err != nil {
			return nil, err
		}
		opts = append(opts, sdktrace.WithBatcher(exporter, sdktrace.WithExportTimeout(cfg.ExportTimeout)))
	}

	return sdktrace.NewTracerProvider(opts...), nil
}

func newExporter(ctx context.Context, cfg Config) (*otlptrace.Exporter, error) {
	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint)}
	if cfg.OTLPInsecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("observability: build OTLP exporter for %s: %w", cfg.OTLPEndpoint, err)
	}

	return exporter, nil
}

// sampler is parent-based, so the decision made where a trace started is
// honoured everywhere it travels. Sampling per service independently is how
// traces come back from the backend with the middle missing — the one shape
// that makes a distributed trace worse than no trace at all.
//
// The ratio therefore only governs traces this process itself begins, which for
// most services means the ones with no inbound request behind them: relays,
// consumers, cron jobs.
func sampler(cfg Config) sdktrace.Sampler {
	switch {
	case cfg.SampleRatio <= 0:
		return sdktrace.ParentBased(sdktrace.NeverSample())
	case cfg.SampleRatio >= 1:
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	default:
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))
	}
}

// propagator carries the trace across every hop.
//
// W3C trace context is what puts traceparent on gRPC metadata and, through the
// outbox headers, on Kafka messages — without it the trace ends at the first
// async boundary. Baggage rides alongside so a value set at the edge reaches
// services several hops away without being threaded through every signature.
func propagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}
