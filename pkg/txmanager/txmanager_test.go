package txmanager_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres/postgrestest"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
)

// A transaction manager is only worth testing against a real server: commit,
// rollback, and the visibility of uncommitted rows are the server's behaviour,
// and a stand-in for the driver would be asserting on itself.
const schema = `CREATE TABLE notes (id text PRIMARY KEY)`

// noteID is arbitrary — every test gets a database to itself, so there is
// nothing to collide with.
const noteID = "note"

func setup(t *testing.T) (*pgxpool.Pool, *txmanager.Manager) {
	t.Helper()

	pool := postgrestest.New(t, schema)

	return pool, txmanager.New(pool)
}

// insert writes a note through whatever txmanager decides ctx should use, the
// way a repository does.
func insert(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := txmanager.From(ctx, pool).Exec(ctx, `INSERT INTO notes (id) VALUES ($1)`, id)

	return err
}

// exists reports whether a note is visible to ctx: committed rows for a plain
// context, and the current transaction's own uncommitted rows inside Do.
func exists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) bool {
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

func TestDoCommits(t *testing.T) {
	ctx := context.Background()
	pool, manager := setup(t)

	if err := manager.Do(ctx, func(ctx context.Context) error {
		return insert(ctx, pool, noteID)
	}); err != nil {
		t.Fatalf("Do() error = %v, want nil", err)
	}

	if !exists(t, ctx, pool, noteID) {
		t.Error("note is missing after Do returned nil")
	}
}

func TestDoRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	pool, manager := setup(t)
	wantErr := errors.New("use case failed")

	err := manager.Do(ctx, func(ctx context.Context) error {
		if err := insert(ctx, pool, noteID); err != nil {
			return err
		}

		return wantErr
	})

	if !errors.Is(err, wantErr) {
		t.Errorf("Do() error = %v, want %v", err, wantErr)
	}
	if exists(t, ctx, pool, noteID) {
		t.Error("note survived a failed Do")
	}
}

func TestDoRollsBackOnPanic(t *testing.T) {
	ctx := context.Background()
	pool, manager := setup(t)

	func() {
		defer func() {
			if p := recover(); p != "boom" {
				t.Errorf("recovered %v, want the original panic to propagate", p)
			}
		}()

		_ = manager.Do(ctx, func(ctx context.Context) error {
			if err := insert(ctx, pool, noteID); err != nil {
				return err
			}
			panic("boom")
		})
	}()

	if exists(t, ctx, pool, noteID) {
		t.Error("note survived a panicking Do")
	}
}

func TestDoJoinsAnAlreadyOpenTransaction(t *testing.T) {
	ctx := context.Background()
	pool, manager := setup(t)
	outerID, innerID := "outer", "inner"

	err := manager.Do(ctx, func(ctx context.Context) error {
		if err := insert(ctx, pool, outerID); err != nil {
			return err
		}

		if err := manager.Do(ctx, func(ctx context.Context) error {
			// Reading a row the outer Do has not committed only works from
			// inside its transaction, which is what "joins" has to mean.
			if !exists(t, ctx, pool, outerID) {
				t.Error("nested Do cannot see the outer transaction's writes")
			}

			return insert(ctx, pool, innerID)
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
		if exists(t, ctx, pool, id) {
			t.Errorf("note %q survived, so the nested Do committed separately", id)
		}
	}
}

func TestDoRollsBackWhenTheCallerGivesUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool, manager := setup(t)

	err := manager.Do(ctx, func(ctx context.Context) error {
		if err := insert(ctx, pool, noteID); err != nil {
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
	if exists(t, context.Background(), pool, noteID) {
		t.Error("note survived a cancelled Do")
	}
}

func TestDoReturnsConnectionsToThePool(t *testing.T) {
	ctx := context.Background()
	pool, manager := setup(t)

	// More iterations than MaxConns, so a transaction that failed to release
	// its connection would exhaust the pool and block instead of failing.
	for i := range 12 {
		err := manager.Do(ctx, func(ctx context.Context) error {
			if err := insert(ctx, pool, strconv.Itoa(i)); err != nil {
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
	pool, _ := setup(t)

	if got := txmanager.From(ctx, pool); got != txmanager.DBTX(pool) {
		t.Errorf("From() = %T, want the fallback", got)
	}
}

func TestFromReturnsTheTransactionInsideDo(t *testing.T) {
	ctx := context.Background()
	pool, manager := setup(t)

	err := manager.Do(ctx, func(ctx context.Context) error {
		if got := txmanager.From(ctx, pool); got == txmanager.DBTX(pool) {
			t.Error("From() returned the fallback inside Do")
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Do() error = %v, want nil", err)
	}
}
