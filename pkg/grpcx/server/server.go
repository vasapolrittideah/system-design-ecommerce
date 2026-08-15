// Package server builds the gRPC server every service in this repo listens on.
//
// No service assembles its own interceptor chain, because an order that drifts
// per service is a class of bug nobody finds until production. It is decided
// once, here:
//
//	recovery → otel → logging → metrics → auth → validate → handler
//
// otel is installed as a stats handler rather than an interceptor, its
// interceptor form being deprecated upstream. That wraps the chain instead of
// sitting inside it, which is the better position anyway: the span exists before
// recovery runs, so a panic lands on the trace rather than beside it.
//
// Typical wiring in cmd/server/main.go:
//
//	cfg := config.MustLoad[server.Config](config.WithPrefix("ORDER_GRPC_"))
//	srv := server.MustNew(cfg, server.WithLogger(log))
//	orderv1.RegisterOrderServiceServer(srv.Registrar(), handler)
//	if err := srv.Serve(ctx); err != nil { ... }
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"buf.build/go/protovalidate"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric/noop"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

// Config is the environment-driven server configuration. Services load it with
// pkg/config, conventionally under a "<SERVICE>_GRPC_" prefix.
type Config struct {
	// Addr is the listen address. It stays a wildcard bind because the pod, not
	// the process, decides which interface is reachable.
	Addr string `env:"ADDR" envDefault:":50051"`

	// MaxRecvMsgSize caps an inbound message. Kong already bounds request
	// bodies at the edge; this bounds what one service can send another, so a
	// runaway batch call fails fast instead of driving the callee out of memory.
	MaxRecvMsgSize int `env:"MAX_RECV_MSG_SIZE" envDefault:"4194304"`

	// MaxSendMsgSize caps an outbound message. Batch read methods are the ones
	// that grow into this: a GetXByIDs with an unbounded id list produces an
	// unbounded response.
	MaxSendMsgSize int `env:"MAX_SEND_MSG_SIZE" envDefault:"4194304"`

	// MaxConcurrentStreams bounds in-flight RPCs per connection. With
	// client-side load balancing a single caller holds one connection per
	// replica, so this is the per-caller concurrency ceiling.
	MaxConcurrentStreams uint32 `env:"MAX_CONCURRENT_STREAMS" envDefault:"250"`

	// ConnectionTimeout bounds how long a half-finished connection setup may
	// occupy a slot.
	ConnectionTimeout time.Duration `env:"CONNECTION_TIMEOUT" envDefault:"10s"`

	// MaxConnectionAge forces clients to reconnect periodically.
	//
	// This is what makes scaling work. gRPC connections are long-lived, so
	// replicas that come up after a caller has already resolved the backend set
	// receive nothing at all until something makes that caller re-resolve.
	// Ageing connections out is that something.
	MaxConnectionAge time.Duration `env:"MAX_CONNECTION_AGE" envDefault:"30m"`

	// MaxConnectionAgeGrace is how long an aged-out connection may keep serving
	// its in-flight calls before it is closed underneath them.
	MaxConnectionAgeGrace time.Duration `env:"MAX_CONNECTION_AGE_GRACE" envDefault:"10s"`

	// KeepaliveTime is how often the server pings an idle connection, so a
	// silently dropped peer is discovered before the next call is sent into it.
	KeepaliveTime time.Duration `env:"KEEPALIVE_TIME" envDefault:"30s"`

	// KeepaliveTimeout is how long a keepalive ping may go unanswered before
	// the connection is considered dead.
	KeepaliveTimeout time.Duration `env:"KEEPALIVE_TIMEOUT" envDefault:"10s"`

	// MinClientPingInterval is the fastest client ping the server tolerates
	// before it treats the peer as abusive and sends GOAWAY. It must stay below
	// the client's own keepalive interval, or well-behaved callers get
	// disconnected for doing exactly what they were configured to do.
	MinClientPingInterval time.Duration `env:"MIN_CLIENT_PING_INTERVAL" envDefault:"10s"`

	// ShutdownTimeout bounds the graceful stop. It has to stay under the pod's
	// terminationGracePeriodSeconds (30s), otherwise the kubelet's SIGKILL
	// arrives first and the graceful path never completes.
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"25s"`

	// Reflection serves the server reflection API, which is what lets grpcurl
	// and similar tools call the service without a copy of the protos. Useful
	// on a laptop, an unnecessary disclosure of the API surface in production.
	Reflection bool `env:"REFLECTION" envDefault:"true"`
}

// Option customizes construction beyond what the environment expresses.
type Option func(*options)

type options struct {
	logger     *zap.Logger
	registerer prometheus.Registerer
	auth       Authenticator
	unary      []grpc.UnaryServerInterceptor
	stream     []grpc.StreamServerInterceptor
}

// WithLogger sets the logger the chain writes through and installs into every
// request context. Without it the chain falls back to zap's global, which is a
// no-op until logger.SetGlobal has been called.
func WithLogger(log *zap.Logger) Option {
	return func(o *options) {
		o.logger = log
	}
}

// WithRegisterer registers the server metrics somewhere other than the default
// Prometheus registry. Two servers in one process need separate registries;
// so do tests, which would otherwise panic on duplicate registration.
func WithRegisterer(reg prometheus.Registerer) Option {
	return func(o *options) {
		o.registerer = reg
	}
}

// WithAuth replaces the default identity extraction with the service's own
// authentication.
//
// The default trusts the identity metadata forwarded from the edge, which is
// the right call for a service reachable only from inside the cluster. The
// Composition API is not that: it is the first hop that a caller can reach, so
// it verifies the JWT itself and passes the result in here.
func WithAuth(fn Authenticator) Option {
	return func(o *options) {
		o.auth = fn
	}
}

// WithUnaryInterceptor appends service-specific interceptors after the standard
// chain, where they run closest to the handler. Idempotency-key handling is the
// intended use: it is per-service policy, but it is not handler code.
func WithUnaryInterceptor(interceptors ...grpc.UnaryServerInterceptor) Option {
	return func(o *options) {
		o.unary = append(o.unary, interceptors...)
	}
}

// WithStreamInterceptor is WithUnaryInterceptor for streaming methods.
func WithStreamInterceptor(interceptors ...grpc.StreamServerInterceptor) Option {
	return func(o *options) {
		o.stream = append(o.stream, interceptors...)
	}
}

// Server is a configured gRPC server together with the listener it will serve
// on and the health service it reports through.
type Server struct {
	grpc   *grpc.Server
	health *health.Server
	lis    net.Listener
	log    *zap.Logger
	cfg    Config
}

// New builds the server and binds cfg.Addr.
//
// Binding here rather than in Serve is deliberate: a port already in use is a
// configuration mistake, and it should surface while main is still wiring
// things up rather than after the pool, the consumers, and the relay have all
// been started.
func New(cfg Config, opts ...Option) (*Server, error) {
	o := options{
		logger:     zap.L(),
		registerer: prometheus.DefaultRegisterer,
		auth:       MetadataIdentity,
	}
	for _, opt := range opts {
		opt(&o)
	}

	metrics, err := newMetrics(o.registerer)
	if err != nil {
		return nil, err
	}

	validator, err := protovalidate.New()
	if err != nil {
		return nil, fmt.Errorf("grpcx/server: build validator: %w", err)
	}

	unary := append([]grpc.UnaryServerInterceptor{
		recoveryUnary(o.logger, metrics),
		loggingUnary(o.logger),
		metricsUnary(metrics),
		authUnary(o.auth),
		validateUnary(validator),
	}, o.unary...)

	stream := append([]grpc.StreamServerInterceptor{
		recoveryStream(o.logger, metrics),
		loggingStream(o.logger),
		metricsStream(metrics),
		authStream(o.auth),
		validateStream(validator),
	}, o.stream...)

	srv := grpc.NewServer(
		// Tracing only. otelgrpc would otherwise publish its own rpc.server.*
		// metrics alongside the grpc_server_* set above — two views of the same
		// calls, disagreeing on naming and buckets, doubling the series.
		grpc.StatsHandler(otelgrpc.NewServerHandler(otelgrpc.WithMeterProvider(noop.NewMeterProvider()))),
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
		grpc.MaxRecvMsgSize(cfg.MaxRecvMsgSize),
		grpc.MaxSendMsgSize(cfg.MaxSendMsgSize),
		grpc.MaxConcurrentStreams(cfg.MaxConcurrentStreams),
		grpc.ConnectionTimeout(cfg.ConnectionTimeout),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionAge:      cfg.MaxConnectionAge,
			MaxConnectionAgeGrace: cfg.MaxConnectionAgeGrace,
			Time:                  cfg.KeepaliveTime,
			Timeout:               cfg.KeepaliveTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime: cfg.MinClientPingInterval,
			// Callers hold idle connections open between bursts, and those are
			// exactly the ones worth pinging.
			PermitWithoutStream: true,
		}),
	)

	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(srv, healthSrv)
	if cfg.Reflection {
		reflection.Register(srv)
	}

	lis, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("grpcx/server: listen on %s: %w", cfg.Addr, err)
	}

	return &Server{
		grpc:   srv,
		health: healthSrv,
		lis:    lis,
		log:    o.logger,
		cfg:    cfg,
	}, nil
}

// MustNew is New for main and bootstrap wiring, where a server that cannot be
// built or bound means the process has nothing to serve and should not start.
func MustNew(cfg Config, opts ...Option) *Server {
	srv, err := New(cfg, opts...)
	if err != nil {
		panic(err)
	}

	return srv
}

// Registrar is where generated RegisterXServer functions attach their handlers.
// It is an interface rather than the concrete server so nothing downstream can
// reconfigure the chain after the fact.
func (s *Server) Registrar() grpc.ServiceRegistrar {
	return s.grpc
}

// Health is the standard gRPC health service. Serve flips the overall status,
// so this is for per-service statuses a process wants to control itself.
func (s *Server) Health() *health.Server {
	return s.health
}

// Addr is the address actually bound, which is what a ":0" listen resolved to.
func (s *Server) Addr() net.Addr {
	return s.lis.Addr()
}

// Serve accepts connections until ctx is cancelled, then stops gracefully.
//
// Shutdown order matters as much as startup order. Health flips to NOT_SERVING
// first, so readiness probes fail and callers stop routing new work here while
// the connections they already hold keep working. Only then does GracefulStop
// drain the in-flight calls. Skipping the first step turns every rolling deploy
// into a burst of Unavailable at the callers.
func (s *Server) Serve(ctx context.Context) error {
	served := make(chan error, 1)
	go func() {
		err := s.grpc.Serve(s.lis)
		if errors.Is(err, grpc.ErrServerStopped) {
			err = nil
		}
		served <- err
	}()

	s.health.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	s.log.Info("grpc server listening", zap.String("addr", s.lis.Addr().String()))

	select {
	case err := <-served:
		if err != nil {
			return fmt.Errorf("grpcx/server: serve: %w", err)
		}

		return nil
	case <-ctx.Done():
	}

	s.health.Shutdown()
	s.log.Info("grpc server draining", zap.Duration("timeout", s.cfg.ShutdownTimeout))

	stopped := make(chan struct{})
	go func() {
		s.grpc.GracefulStop()
		close(stopped)
	}()

	timer := time.NewTimer(s.cfg.ShutdownTimeout)
	defer timer.Stop()

	select {
	case <-stopped:
	case <-timer.C:
		// A call that has not finished by now is not going to. Cutting it off
		// is better than being SIGKILLed mid-drain, which loses the in-flight
		// calls anyway and skips every remaining shutdown step.
		s.log.Warn("grpc server drain timed out, forcing stop")
		s.grpc.Stop()
		<-stopped
	}

	if err := <-served; err != nil {
		return fmt.Errorf("grpcx/server: serve: %w", err)
	}

	return nil
}
