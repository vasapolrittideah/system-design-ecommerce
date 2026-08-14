// Package postgrestest gives tests a real PostgreSQL to run against.
//
// A test asks for a pool and the schema it needs:
//
//	pool := postgrestest.New(t, string(ddl))
//
// and gets a database of its own, created inside a container shared by every
// test in the binary. Starting one container per package keeps the cost to a
// few seconds; giving each caller its own database keeps tests from seeing each
// other's rows without anyone having to remember to clean up.
//
// The container is not stopped explicitly — testcontainers' reaper removes it
// when the test process exits, including when a test panics.
package postgrestest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres"
)

const (
	image    = "postgres:17-alpine"
	user     = "postgrestest"
	password = "postgrestest"
	adminDB  = "postgrestest"
)

// shared starts the container the first time any test asks for a pool, and
// hands every later caller the same one.
var shared = sync.OnceValues(start)

// dbSeq disambiguates databases when the same test asks for more than one, or
// when two subtests sanitize down to the same name.
var dbSeq atomic.Uint64

type instance struct {
	host  string
	port  int
	admin *pgxpool.Pool
}

// New creates a database, applies ddl to it, and returns a pool onto it. Each
// statement in ddl may contain several SQL commands, so a migration file can be
// passed in whole.
func New(tb testing.TB, ddl ...string) *pgxpool.Pool {
	tb.Helper()

	inst, err := shared()
	if err != nil {
		tb.Fatalf("postgrestest: start postgres: %v", err)
	}

	ctx := context.Background()
	name := databaseName(tb)

	if _, err := inst.admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		tb.Fatalf("postgrestest: create database %q: %v", name, err)
	}

	pool, err := postgres.New(ctx, config(inst, name))
	if err != nil {
		tb.Fatalf("postgrestest: open pool on %q: %v", name, err)
	}
	tb.Cleanup(pool.Close)

	for i, stmt := range ddl {
		// The simple protocol is what allows more than one command per call;
		// the extended protocol pgx defaults to rejects them.
		if _, err := pool.Exec(ctx, stmt, pgx.QueryExecModeSimpleProtocol); err != nil {
			tb.Fatalf("postgrestest: apply ddl[%d]: %v", i, err)
		}
	}

	return pool
}

func start() (*instance, error) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase(adminDB),
		tcpostgres.WithUsername(user),
		tcpostgres.WithPassword(password),
		testcontainers.WithWaitStrategy(
			// initdb runs, the server comes up, and it restarts, so the
			// readiness line has to appear twice before the port is usable.
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("run container: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("container host: %w", err)
	}

	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return nil, fmt.Errorf("container port: %w", err)
	}

	inst := &instance{host: host, port: int(port.Num())}

	inst.admin, err = postgres.New(ctx, config(inst, adminDB))
	if err != nil {
		return nil, fmt.Errorf("open admin pool: %w", err)
	}

	return inst, nil
}

func config(inst *instance, database string) postgres.Config {
	return postgres.Config{
		Host:              inst.host,
		Port:              inst.port,
		User:              user,
		Password:          password,
		Database:          database,
		SSLMode:           "disable",
		MaxConns:          4,
		MinConns:          1,
		MaxConnLifetime:   30 * time.Minute,
		MaxConnIdleTime:   5 * time.Minute,
		HealthCheckPeriod: time.Minute,
		ConnectTimeout:    10 * time.Second,
	}
}

// databaseName turns a test name into something PostgreSQL accepts while
// leaving enough of the original to recognise in \l or in pg_stat_activity.
func databaseName(tb testing.TB) string {
	var b strings.Builder
	for _, r := range strings.ToLower(tb.Name()) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}

	name := b.String()
	if len(name) > 40 {
		name = name[:40]
	}

	return fmt.Sprintf("%s_%d", name, dbSeq.Add(1))
}
