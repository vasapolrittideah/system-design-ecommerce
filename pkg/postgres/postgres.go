// Package postgres opens the connection pool every repository in this repo
// reads and writes through.
//
// It owns pool lifecycle and nothing else: no queries, no migrations — those
// run as a Job before the process starts, never from application code — and no
// transaction handling, which belongs to pkg/txmanager.
//
// Typical wiring in cmd/<x>/main.go:
//
//	cfg := config.MustLoad[postgres.Config](config.WithPrefix("ORDER_DB_"))
//	pool := postgres.MustNew(ctx, cfg, postgres.WithLogger(log))
//	defer pool.Close()
//
// From there the pool is passed down explicitly; repositories take it as a
// dependency rather than reaching for a package-level handle.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
)

// Config is the environment-driven pool configuration. Services load it with
// pkg/config, conventionally under a "<SERVICE>_DB_" prefix so a process that
// talks to more than one database can reuse this struct.
type Config struct {
	// Host is the database host. In a cluster this is a Service name, so
	// failover happens behind it without the pool knowing.
	Host string `env:"HOST,required"`

	// Port is the database port.
	Port int `env:"PORT" envDefault:"5432"`

	// User is the role to connect as. Every service connects as its own role,
	// which is how "database per service" is enforced by the server rather than
	// by convention.
	User string `env:"USER,required"`

	// Password comes from a secret, never from the image or a config map, and
	// is config.Secret rather than string so that printing the pool config —
	// the obvious thing to do when a connection fails — cannot print it.
	Password config.Secret `env:"PASSWORD,required"`

	// Database is the database name.
	Database string `env:"DATABASE,required"`

	// SSLMode defaults to require, so a misconfigured deployment fails to
	// connect instead of quietly sending credentials in the clear. Local
	// development sets it to disable explicitly.
	SSLMode string `env:"SSLMODE" envDefault:"require"`

	// MaxConns is the pool ceiling per process, not per service: what reaches
	// the server is replicas × MaxConns, and it has to stay under
	// max_connections with room for migrations and operators. A low ceiling
	// queues requests; a high one locks everyone out at the worst moment.
	MaxConns int32 `env:"MAX_CONNS" envDefault:"10"`

	// MinConns is how many connections stay open while idle, so a burst after a
	// quiet period does not pay the TLS handshake on every request.
	MinConns int32 `env:"MIN_CONNS" envDefault:"2"`

	// MaxConnLifetime caps how long a connection is reused. Without it,
	// connections opened before a failover keep talking to whichever backend
	// they were pinned to and never rediscover the new primary.
	MaxConnLifetime time.Duration `env:"MAX_CONN_LIFETIME" envDefault:"30m"`

	// MaxConnIdleTime releases connections the pool is no longer using, letting
	// a scaled-out deployment give capacity back to the server between peaks.
	MaxConnIdleTime time.Duration `env:"MAX_CONN_IDLE_TIME" envDefault:"5m"`

	// HealthCheckPeriod is how often the pool prunes dead connections and tops
	// itself back up to MinConns.
	HealthCheckPeriod time.Duration `env:"HEALTH_CHECK_PERIOD" envDefault:"1m"`

	// ConnectTimeout bounds a single dial and one startup ping. Zero means no
	// timeout, which turns an unreachable database into a process that hangs
	// forever instead of one that reports why it cannot start.
	ConnectTimeout time.Duration `env:"CONNECT_TIMEOUT" envDefault:"5s"`

	// ConnectMaxWait is how long New keeps retrying that ping before giving up.
	// It covers a database that is not reachable *yet* rather than one that is
	// misconfigured — an instance still starting, or a pod whose NetworkPolicy
	// the CNI has not finished programming — where the process would otherwise
	// die milliseconds into a window that closes on its own. Zero pings once.
	ConnectMaxWait time.Duration `env:"CONNECT_MAX_WAIT" envDefault:"30s"`
}

// Option customizes construction beyond what the environment expresses.
type Option func(*options)

type options struct {
	logger *zap.Logger
}

// WithLogger sets the logger a retried startup ping is reported through. Without
// it those lines go to zap's global, which is a no-op until logger.SetGlobal has
// been called — and a database that took twenty seconds to answer then looks
// like a process that took twenty seconds to start.
func WithLogger(log *zap.Logger) Option {
	return func(o *options) {
		o.logger = log
	}
}

// The delay before a second startup ping, doubling up to connectBackoffMax.
const (
	connectBackoff    = 100 * time.Millisecond
	connectBackoffMax = 2 * time.Second
)

// New opens the pool and verifies it can reach the database before returning.
//
// The ping is what makes a bad host, a rotated password, or a missing database
// a startup failure rather than an error on the first request that happens to
// arrive after the process has already reported itself healthy.
func New(ctx context.Context, cfg Config, opts ...Option) (*pgxpool.Pool, error) {
	o := options{logger: zap.L()}
	for _, opt := range opts {
		opt(&o)
	}

	poolCfg, err := poolConfig(cfg)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: open pool: %w", err)
	}

	if err := awaitReady(ctx, pool, cfg, o.logger); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping %s:%d/%s: %w", cfg.Host, cfg.Port, cfg.Database, err)
	}

	return pool, nil
}

// awaitReady pings until the database answers, ConnectMaxWait runs out, or the
// caller's context is done.
//
// An error the server itself produced ends it immediately: a rejected password
// or a database that does not exist is configuration, and retrying it only
// delays the report by ConnectMaxWait. Everything else is a failure to reach a
// server at all, which at startup is as often a statement about how old the
// process is as about the deployment.
func awaitReady(ctx context.Context, pool *pgxpool.Pool, cfg Config, log *zap.Logger) error {
	// The database is named on every line, since a process that opens two pools
	// would otherwise report the same retry twice with nothing to tell them
	// apart. The password is not: it lives in the DSN, never in a field.
	log = log.With(
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.String("database", cfg.Database),
	)

	started := time.Now()
	deadline := started.Add(cfg.ConnectMaxWait)
	backoff := connectBackoff

	for attempt := 1; ; attempt++ {
		err := ping(ctx, pool, cfg.ConnectTimeout)
		if err == nil {
			// Only after a retry, so an ordinary startup logs nothing and the
			// line that does appear is the one closing a warning above it.
			if attempt > 1 {
				log.Info("postgres reachable",
					zap.Int("attempts", attempt),
					zap.Duration("waited", time.Since(started).Round(time.Millisecond)),
				)
			}

			return nil
		}

		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) || ctx.Err() != nil {
			return err
		}

		if !time.Now().Add(backoff).Before(deadline) {
			if attempt == 1 {
				return err
			}

			return fmt.Errorf("unreachable after %d attempts in %s: %w",
				attempt, time.Since(started).Round(time.Millisecond), err)
		}

		// Warn rather than error: the process is still starting and this may
		// resolve itself, and a level that says "broken" here would fire the
		// error-rate alert on every deploy that outruns its database.
		log.Warn("postgres unreachable, retrying",
			zap.Int("attempt", attempt),
			zap.Duration("retry_in", backoff),
			zap.Error(err),
		)

		select {
		case <-ctx.Done():
			return err
		case <-time.After(backoff):
		}

		backoff = min(backoff*2, connectBackoffMax)
	}
}

// ping runs one attempt under its own timeout, so a dial that hangs costs
// ConnectTimeout rather than the whole retry budget.
func ping(ctx context.Context, pool *pgxpool.Pool, timeout time.Duration) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	return pool.Ping(ctx)
}

// MustNew is New for main and bootstrap wiring, where a database the process
// cannot reach means it has nothing to serve and should not start.
func MustNew(ctx context.Context, cfg Config, opts ...Option) *pgxpool.Pool {
	pool, err := New(ctx, cfg, opts...)
	if err != nil {
		panic(err)
	}

	return pool
}

// poolConfig translates Config into pgx's own configuration.
func poolConfig(cfg Config) (*pgxpool.Config, error) {
	if cfg.MaxConns < 1 {
		return nil, fmt.Errorf("postgres: MaxConns is %d, want at least 1", cfg.MaxConns)
	}
	// pgx tops the pool up to MinConns without checking it against the ceiling,
	// so an inverted pair would spend the process lifetime opening connections
	// it immediately has to close.
	if cfg.MinConns < 0 || cfg.MinConns > cfg.MaxConns {
		return nil, fmt.Errorf("postgres: MinConns is %d, want between 0 and MaxConns (%d)", cfg.MinConns, cfg.MaxConns)
	}

	poolCfg, err := pgxpool.ParseConfig(dsn(cfg))
	if err != nil {
		return nil, fmt.Errorf("postgres: parse connection string: %w", err)
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolCfg.HealthCheckPeriod = cfg.HealthCheckPeriod
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	// Connections opened at startup would otherwise all expire in the same
	// instant and reconnect as one burst, which is exactly the moment a pod is
	// least able to absorb it.
	poolCfg.MaxConnLifetimeJitter = cfg.MaxConnLifetime / 10

	return poolCfg, nil
}

// dsn builds the connection string. It goes through net/url so a password
// containing a reserved character is escaped rather than silently truncating
// the string at the first "@" or "/".
//
// The result carries the password in clear, and pgxpool keeps it reachable
// through pool.Config().ConnString() — the redaction config.Secret gives is gone
// once the string has left this function.
func dsn(cfg Config) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password.Reveal()),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:     "/" + cfg.Database,
		RawQuery: url.Values{"sslmode": []string{cfg.SSLMode}}.Encode(),
	}

	return u.String()
}
