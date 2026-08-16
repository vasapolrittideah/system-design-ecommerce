package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric/noop"
	"go.uber.org/zap"
)

// ServerConfig is the environment-driven server configuration. A BFF loads it
// with pkg/config, conventionally under a "<SERVICE>_HTTP_" prefix.
type ServerConfig struct {
	// Addr is the listen address, a wildcard bind because the pod decides which
	// interface is reachable, not the process.
	Addr string `env:"ADDR" envDefault:":8080"`

	// ReadHeaderTimeout bounds how long a client may take to send its headers.
	// It is the defence against a slowloris holding connections open, and it is
	// the one timeout here with no legitimate reason to be generous.
	ReadHeaderTimeout time.Duration `env:"READ_HEADER_TIMEOUT" envDefault:"5s"`

	// ReadTimeout bounds headers plus body.
	ReadTimeout time.Duration `env:"READ_TIMEOUT" envDefault:"10s"`

	// WriteTimeout bounds how long a handler may take to write its response.
	//
	// It must stay comfortably above the per-request budget [Timeout] applies,
	// because the two fail very differently: the budget expires inside the
	// handler and becomes an error body the client can read, while this one
	// severs the connection and tells the client nothing. This is the backstop
	// for a handler that ignored its context, not the budget itself.
	WriteTimeout time.Duration `env:"WRITE_TIMEOUT" envDefault:"15s"`

	// IdleTimeout is how long a keep-alive connection may sit unused.
	IdleTimeout time.Duration `env:"IDLE_TIMEOUT" envDefault:"120s"`

	// MaxHeaderBytes caps the request head. Bodies are capped separately, at
	// [MaxBodyBytes], because only a handler that reads one knows it is coming.
	MaxHeaderBytes int `env:"MAX_HEADER_BYTES" envDefault:"1048576"`

	// ShutdownTimeout bounds the graceful drain. It has to stay under the pod's
	// terminationGracePeriodSeconds minus the preStop sleep, or the kubelet's
	// SIGKILL lands mid-drain and none of this runs.
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"25s"`
}

// ServerOption customizes construction beyond what the environment expresses.
type ServerOption func(*serverOptions)

type serverOptions struct {
	logger *zap.Logger
}

// WithServerLogger sets the logger the lifecycle lines are written through.
// Without it they go to zap's global, which is a no-op until logger.SetGlobal
// has been called.
func WithServerLogger(log *zap.Logger) ServerOption {
	return func(o *serverOptions) {
		o.logger = log
	}
}

// Server is a configured HTTP server together with the listener it will serve
// on.
type Server struct {
	http *http.Server
	lis  net.Listener
	log  *zap.Logger
	cfg  ServerConfig
}

// NewServer builds the server around handler and binds cfg.Addr.
//
// Binding here rather than in Serve is deliberate: a port already in use is a
// configuration mistake, and it should surface while main is still wiring things
// up rather than after every downstream connection has been dialled.
//
// The handler is wrapped in otel instrumentation, which puts it outside the
// middleware chain [MustNewRouter] builds — the same position otelgrpc holds on the
// gRPC side, and for the same reason: the span exists before recovery runs, so a
// panic lands on the trace rather than beside it. This is also where a trace
// begins for the whole system, since the BFF is the first hop that a request
// reaches.
func NewServer(cfg ServerConfig, handler http.Handler, opts ...ServerOption) (*Server, error) {
	o := serverOptions{logger: zap.L()}
	for _, opt := range opts {
		opt(&o)
	}

	lis, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("httpx: listen on %s: %w", cfg.Addr, err)
	}

	return &Server{
		http: &http.Server{
			// Tracing only. The http_server_* set below comes from the metrics
			// middleware, and otelhttp's own http.server.* metrics would be a
			// second view of the same requests under a different naming scheme.
			Handler:           otelhttp.NewHandler(handler, "http.server", otelhttp.WithMeterProvider(noop.NewMeterProvider())),
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			ReadTimeout:       cfg.ReadTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
			MaxHeaderBytes:    cfg.MaxHeaderBytes,
		},
		lis: lis,
		log: o.logger,
		cfg: cfg,
	}, nil
}

// MustNewServer is NewServer for main and bootstrap wiring, where a server that
// cannot bind means the process has nothing to serve.
func MustNewServer(cfg ServerConfig, handler http.Handler, opts ...ServerOption) *Server {
	srv, err := NewServer(cfg, handler, opts...)
	if err != nil {
		panic(err)
	}

	return srv
}

// Addr is the address actually bound, which is what a ":0" listen resolved to.
func (s *Server) Addr() net.Addr {
	return s.lis.Addr()
}

// Serve accepts requests until ctx is cancelled, then drains.
//
// Shutdown stops accepting new connections and waits for the in-flight ones,
// which is why the pod's preStop sleep matters: without that pause the process
// can stop accepting before the last caller has been told this pod left the
// endpoint list, and those requests fail rather than drain.
func (s *Server) Serve(ctx context.Context) error {
	served := make(chan error, 1)
	go func() {
		err := s.http.Serve(s.lis)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		served <- err
	}()

	s.log.Info("http server listening", zap.String("addr", s.lis.Addr().String()))

	select {
	case err := <-served:
		if err != nil {
			return fmt.Errorf("httpx: serve: %w", err)
		}

		return nil
	case <-ctx.Done():
	}

	s.log.Info("http server draining", zap.Duration("timeout", s.cfg.ShutdownTimeout))

	// Detached from ctx, which is already cancelled — Shutdown given a cancelled
	// context returns immediately and drains nothing.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.ShutdownTimeout)
	defer cancel()

	if err := s.http.Shutdown(shutdownCtx); err != nil {
		// A request still running now is not going to finish. Closing is worse
		// than draining and better than being SIGKILLed, which loses the same
		// requests and skips every remaining shutdown step in main.
		s.log.Warn("http server drain timed out, forcing close", zap.Error(err))
		_ = s.http.Close()
	}

	return <-served
}
