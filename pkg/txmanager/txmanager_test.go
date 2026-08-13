package txmanager_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
)

// pool points at a throwaway PostgreSQL instance shared by every test in this
// package. A transaction manager is only worth testing against a real server:
// commit, rollback, and visibility of uncommitted rows are the server's
// behaviour, and a stand-in for the driver would be asserting on itself.
var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

// run exists so the container and pool are still cleaned up on the way out,
// which os.Exit in TestMain would otherwise skip.
func run(m *testing.M) int {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("txmanager"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			// The server starts, runs initdb, and restarts, so the readiness
			// line has to be seen twice before the port is really usable.
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		log.Printf("start postgres container: %v", err)
		return 1
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("terminate postgres container: %v", err)
		}
	}()

	pool, err = newPool(ctx, container)
	if err != nil {
		log.Printf("open pool: %v", err)
		return 1
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `CREATE TABLE notes (id text PRIMARY KEY)`); err != nil {
		log.Printf("create schema: %v", err)
		return 1
	}

	return m.Run()
}

func newPool(ctx context.Context, container *tcpostgres.PostgresContainer) (*pgxpool.Pool, error) {
	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("container host: %w", err)
	}

	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return nil, fmt.Errorf("container port: %w", err)
	}

	return postgres.New(ctx, postgres.Config{
		Host:              host,
		Port:              int(port.Num()),
		User:              "test",
		Password:          "test",
		Database:          "txmanager",
		SSLMode:           "disable",
		MaxConns:          4,
		MinConns:          1,
		MaxConnLifetime:   30 * time.Minute,
		MaxConnIdleTime:   5 * time.Minute,
		HealthCheckPeriod: time.Minute,
		ConnectTimeout:    5 * time.Second,
	})
}

// insert writes a note through whatever txmanager decides ctx should use, the
// way a repository does.
func insert(ctx context.Context, id string) error {
	_, err := txmanager.From(ctx, pool).Exec(ctx, `INSERT INTO notes (id) VALUES ($1)`, id)

	return err
}

// exists reports whether a note is visible to ctx: committed rows for a plain
// context, and the current transaction's own uncommitted rows inside Do.
func exists(t *testing.T, ctx context.Context, id string) bool {
	t.Helper()

	var count int
	err := txmanager.From(ctx, pool).
		QueryRow(ctx, `SELECT count(*) FROM notes WHERE id = $1`, id).
		Scan(&count)
	if err != nil {
		t.Fatalf("count notes: %v", err)
	}

	return count > 0
}

// noteID keeps tests from colliding in the shared table.
func noteID(t *testing.T) string {
	t.Helper()

	return t.Name()
}

func TestDoCommits(t *testing.T) {
	ctx := context.Background()
	id := noteID(t)

	if err := txmanager.New(pool).Do(ctx, func(ctx context.Context) error {
		return insert(ctx, id)
	}); err != nil {
		t.Fatalf("Do() error = %v, want nil", err)
	}

	if !exists(t, ctx, id) {
		t.Error("note is missing after Do returned nil")
	}
}

func TestDoRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	id := noteID(t)
	wantErr := errors.New("use case failed")

	err := txmanager.New(pool).Do(ctx, func(ctx context.Context) error {
		if err := insert(ctx, id); err != nil {
			return err
		}

		return wantErr
	})

	if !errors.Is(err, wantErr) {
		t.Errorf("Do() error = %v, want %v", err, wantErr)
	}
	if exists(t, ctx, id) {
		t.Error("note survived a failed Do")
	}
}

func TestDoRollsBackOnPanic(t *testing.T) {
	ctx := context.Background()
	id := noteID(t)

	func() {
		defer func() {
			if p := recover(); p != "boom" {
				t.Errorf("recovered %v, want the original panic to propagate", p)
			}
		}()

		_ = txmanager.New(pool).Do(ctx, func(ctx context.Context) error {
			if err := insert(ctx, id); err != nil {
				t.Fatalf("insert: %v", err)
			}
			panic("boom")
		})
	}()

	if exists(t, ctx, id) {
		t.Error("note survived a panicking Do")
	}
}

func TestDoJoinsAnAlreadyOpenTransaction(t *testing.T) {
	ctx := context.Background()
	manager := txmanager.New(pool)
	outerID, innerID := noteID(t)+"-outer", noteID(t)+"-inner"

	err := manager.Do(ctx, func(ctx context.Context) error {
		if err := insert(ctx, outerID); err != nil {
			return err
		}

		if err := manager.Do(ctx, func(ctx context.Context) error {
			// Reading a row the outer Do has not committed only works from
			// inside its transaction, which is what "joins" has to mean.
			if !exists(t, ctx, outerID) {
				t.Error("nested Do cannot see the outer transaction's writes")
			}

			return insert(ctx, innerID)
		}); err != nil {
			return err
		}

		return errors.New("outer failed after the nested Do returned")
	})
	if err == nil {
		t.Fatal("Do() error = nil, want error")
	}

	// The nested Do returning nil must not have committed anything on its own.
	for _, id := range []string{outerID, innerID} {
		if exists(t, ctx, id) {
			t.Errorf("note %q survived, so the nested Do committed separately", id)
		}
	}
}

func TestDoRollsBackWhenTheCallerGivesUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := noteID(t)

	err := txmanager.New(pool).Do(ctx, func(ctx context.Context) error {
		if err := insert(ctx, id); err != nil {
			return err
		}
		cancel()

		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Do() error = %v, want context.Canceled", err)
	}

	// The rollback ran on a cancelled context; the pool has to be left usable
	// and the write must be gone.
	if exists(t, context.Background(), id) {
		t.Error("note survived a cancelled Do")
	}
}

func TestDoReturnsConnectionsToThePool(t *testing.T) {
	ctx := context.Background()
	manager := txmanager.New(pool)

	// More iterations than MaxConns, so a transaction that failed to release
	// its connection would exhaust the pool and block instead of failing.
	for i := range 12 {
		id := noteID(t) + strconv.Itoa(i)

		err := manager.Do(ctx, func(ctx context.Context) error {
			if err := insert(ctx, id); err != nil {
				return err
			}

			return errors.New("rolled back")
		})
		if err == nil {
			t.Fatalf("Do() error = nil on iteration %d, want error", i)
		}
	}

	if got := pool.Stat().AcquiredConns(); got != 0 {
		t.Errorf("AcquiredConns() = %d, want 0", got)
	}
}

func TestFromFallsBackOutsideATransaction(t *testing.T) {
	ctx := context.Background()

	if got := txmanager.From(ctx, pool); got != txmanager.DBTX(pool) {
		t.Errorf("From() = %T, want the fallback", got)
	}
}

func TestFromReturnsTheTransactionInsideDo(t *testing.T) {
	ctx := context.Background()

	err := txmanager.New(pool).Do(ctx, func(ctx context.Context) error {
		if got := txmanager.From(ctx, pool); got == txmanager.DBTX(pool) {
			t.Error("From() returned the fallback inside Do")
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Do() error = %v, want nil", err)
	}
}
