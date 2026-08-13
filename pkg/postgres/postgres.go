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
//	pool := postgres.MustNew(ctx, cfg)
//	defer pool.Close()
//
// From there the pool is passed down explicitly; repositories take it as a
// dependency rather than reaching for a package-level handle.
package postgres

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

	// Password comes from a secret, never from the image or a config map.
	Password string `env:"PASSWORD,required"`

	// Database is the database name.
	Database string `env:"DATABASE,required"`

	// SSLMode defaults to require, so a misconfigured deployment fails to
	// connect instead of quietly sending credentials in the clear. Local
	// development sets it to disable explicitly.
	SSLMode string `env:"SSLMODE" envDefault:"require"`

	// MaxConns is the pool ceiling per process, not per service. The number
	// that reaches the server is replicas × MaxConns, summed over every service
	// sharing the instance, and it has to stay under max_connections with room
	// for migrations and operators. A low ceiling queues requests; a high one
	// locks everyone out of the database at the worst possible moment.
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

	// ConnectTimeout bounds a single dial and the startup ping. Zero means no
	// timeout, which turns an unreachable database into a process that hangs
	// forever instead of one that reports why it cannot start.
	ConnectTimeout time.Duration `env:"CONNECT_TIMEOUT" envDefault:"5s"`
}

// New opens the pool and verifies it can reach the database before returning.
//
// The ping is what makes a bad host, a rotated password, or a missing database
// a startup failure rather than an error on the first request that happens to
// arrive after the process has already reported itself healthy.
func New(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	poolCfg, err := poolConfig(cfg)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: open pool: %w", err)
	}

	if cfg.ConnectTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.ConnectTimeout)
		defer cancel()
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping %s:%d/%s: %w", cfg.Host, cfg.Port, cfg.Database, err)
	}

	return pool, nil
}

// MustNew is New for main and bootstrap wiring, where a database the process
// cannot reach means it has nothing to serve and should not start.
func MustNew(ctx context.Context, cfg Config) *pgxpool.Pool {
	pool, err := New(ctx, cfg)
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
func dsn(cfg Config) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:     "/" + cfg.Database,
		RawQuery: url.Values{"sslmode": []string{cfg.SSLMode}}.Encode(),
	}

	return u.String()
}
