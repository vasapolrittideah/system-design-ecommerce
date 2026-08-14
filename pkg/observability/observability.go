// Package observability starts the telemetry a process needs before it can be
// operated: traces, metrics, and the admin endpoints Kubernetes probes.
//
// It exists because every other package here reads telemetry from a global.
// pkg/grpcx hands otelgrpc to the server, and otelgrpc resolves
// otel.GetTracerProvider at the moment the handler is built — so a process that
// wires its server before its telemetry captures the no-op provider for good.
// No span is ever exported, trace_id is empty on every log line, and nothing
// reports an error. Start therefore runs first, before anything else in main:
//
//	obs := observability.MustStart(ctx, obsCfg, observability.WithLogger(log))
//	defer obs.Shutdown(context.WithoutCancel(ctx))
//
//	srv := server.MustNew(grpcCfg, server.WithLogger(log), server.WithRegisterer(obs.Registry()))
//	obs.AddReadinessCheck("postgres", pool.Ping)
//
// The Prometheus registry is owned here and passed down explicitly rather than
// reached for through prometheus.DefaultRegisterer, so two servers in one
// process — or two tests in one package — cannot collide over global state.
package observability

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	// Pinned to the schema resource.Default() carries in this SDK version.
	// resource.Merge refuses to combine two different schema URLs, so bumping
	// the SDK without bumping this line fails at startup.
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.uber.org/zap"
)

// Config is the environment-driven telemetry configuration. Services load it
// with pkg/config, conventionally under an "OBS_" prefix.
type Config struct {
	// Service names the emitting service on every span and on target_info. It
	// has no default for the same reason pkg/logger's does not: telemetry that
	// cannot be attributed to a service is close to useless once several
	// services share a backend.
	Service string `env:"SERVICE,required"`

	// Version identifies the build, so a latency regression can be tied to a
	// rollout.
	Version string `env:"VERSION" envDefault:"dev"`

	// TracingEnabled turns span export off without removing the wiring. With it
	// off the process still propagates context — traceparent still travels over
	// gRPC metadata and Kafka headers — so a service that does not export spans
	// does not break the traces of the ones that do.
	TracingEnabled bool `env:"TRACING_ENABLED" envDefault:"true"`

	// OTLPEndpoint is the collector's OTLP/gRPC address, host:port with no
	// scheme. Jaeger and Tempo both speak it.
	OTLPEndpoint string `env:"OTLP_ENDPOINT" envDefault:"localhost:4317"`

	// OTLPInsecure sends to the collector without TLS, which is the normal case
	// for a collector running as a sidecar or in-cluster.
	OTLPInsecure bool `env:"OTLP_INSECURE" envDefault:"true"`

	// SampleRatio is the share of traces started here that are recorded. It
	// only applies to traces this process begins: sampling is parent-based, so
	// a trace already sampled upstream is recorded whatever this says, and one
	// already dropped stays dropped. Without that, traces would come back from
	// the backend with holes in the middle.
	SampleRatio float64 `env:"SAMPLE_RATIO" envDefault:"0.1"`

	// ExportTimeout bounds one export attempt to the collector.
	ExportTimeout time.Duration `env:"EXPORT_TIMEOUT" envDefault:"10s"`

	// AdminAddr is where /metrics, /healthz, and /readyz are served. It is a
	// separate port from the service's own so that scraping and probing do not
	// compete with traffic, and so the port can be kept off any public route.
	AdminAddr string `env:"ADMIN_ADDR" envDefault:":9090"`

	// ReadinessTimeout bounds the whole /readyz check. A probe that hangs is
	// read as a failure by the kubelet only after its own timeout, and until
	// then the pod keeps receiving traffic it may not be able to serve.
	ReadinessTimeout time.Duration `env:"READINESS_TIMEOUT" envDefault:"2s"`

	// ShutdownTimeout bounds flushing spans and stopping the admin server.
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"5s"`
}

// Option customizes construction beyond what the environment expresses.
type Option func(*options)

type options struct {
	logger *zap.Logger
}

// WithLogger sets the logger telemetry failures are reported through — a
// collector that cannot be reached, a metric that cannot be registered. Without
// it those go to zap's global, which is a no-op until logger.SetGlobal has been
// called, and silent telemetry failures are the whole problem this package
// exists to avoid.
func WithLogger(log *zap.Logger) Option {
	return func(o *options) {
		o.logger = log
	}
}

// Provider owns everything Start brought up and is the handle main shuts down.
type Provider struct {
	registry *prometheus.Registry
	tracer   *sdktrace.TracerProvider
	meter    *sdkmetric.MeterProvider
	admin    *adminServer
	log      *zap.Logger
	cfg      Config
}

// Start installs the global telemetry providers and brings up the admin server.
//
// It must be called before any package that reads a global provider is
// constructed — in practice, first in main. It does not verify that the
// collector is reachable: spans are exported in the background by a batch
// processor, and a process that refuses to start because a telemetry backend is
// down has turned an observability outage into a service outage.
func Start(ctx context.Context, cfg Config, opts ...Option) (*Provider, error) {
	o := options{logger: zap.L()}
	for _, opt := range opts {
		opt(&o)
	}

	// otel reports export failures and dropped spans through a global handler
	// that writes to stderr by default, outside the structured log stream and
	// therefore outside anything that would alert on it.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		o.logger.Error("opentelemetry error", zap.Error(err))
	}))

	res, err := newResource(cfg)
	if err != nil {
		return nil, err
	}

	p := &Provider{
		registry: newRegistry(),
		log:      o.logger,
		cfg:      cfg,
	}

	if p.meter, err = newMeterProvider(res, p.registry); err != nil {
		return nil, err
	}
	otel.SetMeterProvider(p.meter)

	if p.tracer, err = newTracerProvider(ctx, cfg, res); err != nil {
		return nil, err
	}
	otel.SetTracerProvider(p.tracer)
	otel.SetTextMapPropagator(propagator())

	if p.admin, err = startAdmin(cfg, p.registry, o.logger); err != nil {
		return nil, err
	}

	return p, nil
}

// MustStart is Start for main and bootstrap wiring, where a process that cannot
// be observed should not be running.
func MustStart(ctx context.Context, cfg Config, opts ...Option) *Provider {
	p, err := Start(ctx, cfg, opts...)
	if err != nil {
		panic(err)
	}

	return p
}

// Registry is the Prometheus registry every collector in this process registers
// into. Pass it to grpcx/server with server.WithRegisterer.
func (p *Provider) Registry() *prometheus.Registry {
	return p.registry
}

// AdminAddr is the address the admin server actually bound, which is what a
// ":0" listen resolved to.
func (p *Provider) AdminAddr() string {
	return p.admin.addr()
}

// AddReadinessCheck registers a dependency /readyz will verify.
//
// Checks are added after Start rather than passed into it, because the things
// worth checking — the pool, the Kafka client — do not exist yet at the point
// telemetry has to be running. Until a check is added the endpoint reports
// ready, so register them as each dependency is wired, not at the end.
func (p *Provider) AddReadinessCheck(name string, check func(ctx context.Context) error) {
	p.admin.addReadinessCheck(name, check)
}

// Shutdown flushes pending spans and stops the admin server.
//
// It must be given a context that is not already cancelled — the usual caller
// is a deferred call in main, right after the signal that cancelled everything
// else — or the flush it exists to perform is skipped and the last spans before
// a crash, which are the interesting ones, are lost.
func (p *Provider) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.ShutdownTimeout)
	defer cancel()

	var errs []error
	if err := p.tracer.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("observability: shut down tracing: %w", err))
	}
	if err := p.meter.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("observability: shut down metrics: %w", err))
	}
	// The admin server goes last so a probe arriving mid-drain still gets an
	// answer rather than a connection refused.
	if err := p.admin.shutdown(ctx); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func newResource(cfg Config) (*resource.Resource, error) {
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.Service),
		semconv.ServiceVersion(cfg.Version),
	))
	if err != nil {
		return nil, fmt.Errorf("observability: build resource: %w", err)
	}

	return res, nil
}
