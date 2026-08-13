// Package client dials the gRPC connections services use to talk to each other.
//
// East-west calls are the ones that turn a single slow dependency into an
// outage, so the defensive parts are not left to the caller. A connection built
// here already carries a deadline it cannot exceed, retries that only fire where
// they are safe, a circuit breaker that stops hammering a service that is
// already down, and round-robin balancing across every replica rather than
// whichever one the first connection landed on.
//
// Typical wiring in bootstrap:
//
//	cfg := config.MustLoad[client.Config](config.WithPrefix("STOCK_CLIENT_"))
//	conn := client.MustDial(cfg, client.WithLogger(log))
//	defer conn.Close()
//	stock := stockv1.NewStockServiceClient(conn)
//
// One connection per target service, dialled once at startup and shared: a
// *grpc.ClientConn is safe for concurrent use and manages its own subconnections.
package client

import (
	"fmt"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric/noop"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// roundRobin is the default service config applied to every connection.
//
// Without it gRPC picks the first healthy address and sends everything there,
// which is exactly wrong for a headless Service: the caller resolves all the
// pod IPs and then uses one of them. Setting the policy on the connection
// rather than per-call means a service that scales out starts receiving traffic
// as soon as its callers re-resolve.
const roundRobin = `{"loadBalancingConfig":[{"round_robin":{}}]}`

// Config is the environment-driven client configuration. Services load it with
// pkg/config, one prefix per target — "STOCK_CLIENT_", "PAYMENT_CLIENT_" — so a
// service that calls three others configures each independently.
type Config struct {
	// Target is the gRPC target to dial, e.g. "dns:///stock:50051". The
	// "dns:///" scheme matters: it resolves every backing address and re-resolves
	// as they change, where a bare host:port hands the whole connection to
	// whatever the first lookup returned.
	Target string `env:"TARGET,required"`

	// Timeout is the deadline applied to a call whose caller did not set one.
	//
	// It is a backstop, not the design. The budget cascades — Kong 5s, BFF
	// 800ms, downstream 300ms — and the caller is the one that knows where in
	// that budget it sits. This only guarantees that a forgotten deadline
	// cannot become an unbounded wait.
	Timeout time.Duration `env:"TIMEOUT" envDefault:"300ms"`

	// MaxAttempts is the total number of tries, retries included. One disables
	// retrying entirely.
	MaxAttempts int `env:"MAX_ATTEMPTS" envDefault:"3"`

	// RetryBackoff is the base delay, doubled per attempt and jittered.
	RetryBackoff time.Duration `env:"RETRY_BACKOFF" envDefault:"20ms"`

	// RetryMaxBackoff caps that delay. Both defaults are small on purpose:
	// three attempts have to fit inside Timeout, and a backoff longer than the
	// budget just spends it sleeping.
	RetryMaxBackoff time.Duration `env:"RETRY_MAX_BACKOFF" envDefault:"200ms"`

	// IdempotentMethods names extra methods that are safe to retry, as either a
	// full "/pkg.Service/Method" or a bare "Method". See idempotent for the
	// prefixes that are recognised without being listed.
	IdempotentMethods []string `env:"IDEMPOTENT_METHODS" envSeparator:","`

	// KeepaliveTime is how often an idle connection is pinged. It must stay
	// above the callee's MinClientPingInterval, or the server answers the pings
	// with GOAWAY.
	KeepaliveTime time.Duration `env:"KEEPALIVE_TIME" envDefault:"30s"`

	// KeepaliveTimeout is how long a ping may go unanswered before the
	// connection is torn down and re-established.
	KeepaliveTimeout time.Duration `env:"KEEPALIVE_TIMEOUT" envDefault:"10s"`

	// MaxRecvMsgSize caps an inbound response.
	MaxRecvMsgSize int `env:"MAX_RECV_MSG_SIZE" envDefault:"4194304"`

	// BreakerMinRequests is how many calls must be observed in a window before
	// the breaker is allowed to trip, so a single failure during a quiet period
	// cannot open it.
	BreakerMinRequests uint32 `env:"BREAKER_MIN_REQUESTS" envDefault:"20"`

	// BreakerFailureRatio is the share of failures that opens the breaker.
	BreakerFailureRatio float64 `env:"BREAKER_FAILURE_RATIO" envDefault:"0.5"`

	// BreakerInterval is the window over which those counts are kept.
	BreakerInterval time.Duration `env:"BREAKER_INTERVAL" envDefault:"30s"`

	// BreakerTimeout is how long the breaker stays open before letting probe
	// calls through again.
	BreakerTimeout time.Duration `env:"BREAKER_TIMEOUT" envDefault:"15s"`

	// BreakerHalfOpenRequests is how many probes are allowed through at once
	// while the breaker is testing whether the target has recovered.
	BreakerHalfOpenRequests uint32 `env:"BREAKER_HALF_OPEN_REQUESTS" envDefault:"3"`
}

// Option customizes construction beyond what the environment expresses.
type Option func(*options)

type options struct {
	logger *zap.Logger
}

// WithLogger sets the logger used to report circuit breaker state changes.
// Without it those go to zap's global, which is a no-op until logger.SetGlobal
// has been called — and a breaker opening silently is the one event here worth
// waking up for.
func WithLogger(log *zap.Logger) Option {
	return func(o *options) {
		o.logger = log
	}
}

// Dial builds the connection.
//
// It does not block on connectivity, and should not: a service that refuses to
// start because a dependency is briefly down turns one failure into two, and
// during a rolling deploy every service is briefly down. The connection comes
// up in the background and the first calls made against a still-connecting
// target fail with Unavailable — which is precisely what the retry and the
// breaker are for.
func Dial(cfg Config, opts ...Option) (*grpc.ClientConn, error) {
	o := options{logger: zap.L()}
	for _, opt := range opts {
		opt(&o)
	}

	conn, err := grpc.NewClient(cfg.Target,
		// Plaintext inside the cluster. TLS is terminated by Kong at the edge,
		// and east-west encryption, when it is wanted, comes from the mesh
		// rather than from every service growing its own certificate handling.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		// Tracing only: the callee already publishes the grpc_server_* metrics
		// for these calls, and a second client-side view of the same traffic
		// costs cardinality without answering a new question.
		grpc.WithStatsHandler(otelgrpc.NewClientHandler(otelgrpc.WithMeterProvider(noop.NewMeterProvider()))),
		grpc.WithDefaultServiceConfig(roundRobin),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                cfg.KeepaliveTime,
			Timeout:             cfg.KeepaliveTimeout,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(cfg.MaxRecvMsgSize)),
		// Outermost first. The deadline has to bound everything inside it,
		// including the retries; propagation runs once so every attempt carries
		// the same metadata; and the breaker sits outside the retry loop so one
		// logical call counts as one observation rather than three.
		grpc.WithChainUnaryInterceptor(
			deadlineUnary(cfg.Timeout),
			propagateUnary(),
			breakerUnary(newBreaker(cfg, o.logger)),
			retryUnary(cfg),
		),
		grpc.WithChainStreamInterceptor(propagateStream()),
	)
	if err != nil {
		return nil, fmt.Errorf("grpcx/client: dial %s: %w", cfg.Target, err)
	}

	return conn, nil
}

// MustDial is Dial for main and bootstrap wiring. It panics only on a malformed
// target or configuration, never on an unreachable one — see Dial.
func MustDial(cfg Config, opts ...Option) *grpc.ClientConn {
	conn, err := Dial(cfg, opts...)
	if err != nil {
		panic(err)
	}

	return conn
}
