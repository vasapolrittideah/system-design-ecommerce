package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres/postgrestest"
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/adapter/out/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
)

// migrationsDir is the schema. The test applies the same files goose runs in the
// cluster rather than a copy kept next to it, because a copy is a second schema
// that drifts and reports nothing when it does.
const migrationsDir = "../../../../db/migrations"

// setupStock gives each test its own database with the migrations applied.
//
// A real PostgreSQL, never a fake driver. Everything this repository depends on
// is the server's behaviour: a conditional UPDATE re-evaluating its predicate
// against what a concurrent writer left, a row lock serialising two callers, a
// UNIQUE violation carrying a constraint name, and a partial unique index
// applying to some rows and not others. A mock would agree with whatever this
// code believed.
func setupStock(t *testing.T) (*pgxpool.Pool, *adapter.StockRepository) {
	t.Helper()

	pool := postgrestest.New(t, upMigrations(t)...)

	return pool, adapter.NewStockRepository(pool)
}

// upMigrations returns the Up half of every migration, in order. The Down half
// is cut away: applied together they would create the schema and immediately
// drop it.
func upMigrations(t *testing.T) []string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}

	if len(paths) == 0 {
		t.Fatalf("no migrations found in %s", migrationsDir)
	}

	statements := make([]string, 0, len(paths))

	for _, path := range paths {
		content, err := os.ReadFile(path) //nolint:gosec // a path this test built
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		up, _, _ := strings.Cut(string(content), "-- +goose Down")
		statements = append(statements, up)
	}

	return statements
}

// track starts tracking a SKU at a count, and fails the test if it cannot.
func track(t *testing.T, ctx context.Context, stock *adapter.StockRepository, sku string, available int32) {
	t.Helper()

	if _, err := stock.Create(ctx, domain.NewStockItem(domain.SKU(sku), domain.Quantity(available))); err != nil {
		t.Fatalf("Create(%s) error = %v, want nil", sku, err)
	}
}

// counts reads back what the warehouse holds for one SKU.
func counts(t *testing.T, ctx context.Context, stock *adapter.StockRepository, sku string) (int32, int32) {
	t.Helper()

	items, err := stock.FindBySKUs(ctx, []domain.SKU{domain.SKU(sku)})
	if err != nil {
		t.Fatalf("FindBySKUs(%s) error = %v, want nil", sku, err)
	}

	if len(items) != 1 {
		t.Fatalf("FindBySKUs(%s) = %d items, want 1", sku, len(items))
	}

	return items[0].Available().Int32(), items[0].Reserved().Int32()
}

func TestCreateAndFind(t *testing.T) {
	ctx := context.Background()
	_, stock := setupStock(t)

	track(t, ctx, stock, "SHIRT-M", 5)

	available, reserved := counts(t, ctx, stock, "SHIRT-M")
	if available != 5 || reserved != 0 {
		t.Errorf("counts = %d available, %d reserved, want 5 and 0", available, reserved)
	}

	t.Run("a sku is tracked once", func(t *testing.T) {
		_, err := stock.Create(ctx, domain.NewStockItem("SHIRT-M", 3))
		if got := errorx.KindOf(err); got != errorx.KindConflict {
			t.Fatalf("Create() kind = %q, want %q", got, errorx.KindConflict)
		}

		if got := errorx.Reason(err); got != "SKU_ALREADY_TRACKED" {
			t.Errorf("Create() reason = %q, want SKU_ALREADY_TRACKED", got)
		}
	})

	t.Run("an untracked sku is missing rather than an error", func(t *testing.T) {
		items, err := stock.FindBySKUs(ctx, []domain.SKU{"SHIRT-M", "NEVER-HEARD-OF"})
		if err != nil {
			t.Fatalf("FindBySKUs() error = %v, want nil", err)
		}

		if len(items) != 1 {
			t.Errorf("FindBySKUs() = %d items, want 1 — a missing sku must not fail the batch", len(items))
		}
	})
}

func TestReserve(t *testing.T) {
	ctx := context.Background()

	t.Run("moves available into reserved", func(t *testing.T) {
		_, stock := setupStock(t)
		track(t, ctx, stock, "SHIRT-M", 5)

		if err := stock.Reserve(ctx, "SHIRT-M", 2); err != nil {
			t.Fatalf("Reserve() error = %v, want nil", err)
		}

		available, reserved := counts(t, ctx, stock, "SHIRT-M")
		if available != 3 || reserved != 2 {
			t.Errorf("counts = %d available, %d reserved, want 3 and 2", available, reserved)
		}
	})

	t.Run("refuses more than there is", func(t *testing.T) {
		_, stock := setupStock(t)
		track(t, ctx, stock, "SHIRT-M", 1)

		err := stock.Reserve(ctx, "SHIRT-M", 2)
		if !errors.Is(err, domain.ErrInsufficientStock) {
			t.Fatalf("Reserve() error = %v, want %v", err, domain.ErrInsufficientStock)
		}

		// FailedPrecondition and not Internal, which is what keeps a run of
		// sold-out SKUs from tripping a caller's circuit breaker.
		if got := errorx.KindOf(err); got != errorx.KindConflict {
			t.Errorf("Reserve() kind = %q, want %q", got, errorx.KindConflict)
		}

		if got := errorx.Reason(err); got != "OUT_OF_STOCK" {
			t.Errorf("Reserve() reason = %q, want OUT_OF_STOCK", got)
		}

		// The SKU is in the metadata because a caller reserving a basket has to
		// know which line failed.
		if got := errorx.Metadata(err)["sku"]; got != "SHIRT-M" {
			t.Errorf("Reserve() metadata[sku] = %q, want SHIRT-M", got)
		}

		if available, _ := counts(t, ctx, stock, "SHIRT-M"); available != 1 {
			t.Errorf("available = %d after a refusal, want 1", available)
		}
	})

	t.Run("tells an untracked sku from an empty one", func(t *testing.T) {
		_, stock := setupStock(t)

		err := stock.Reserve(ctx, "NEVER-HEARD-OF", 1)
		if !errors.Is(err, domain.ErrStockItemNotFound) {
			t.Fatalf("Reserve() error = %v, want %v", err, domain.ErrStockItemNotFound)
		}

		// A 404 and not a 409: a caller that meets it has asked about something
		// the warehouse has never held, and waiting will not change that.
		if got := errorx.KindOf(err); got != errorx.KindNotFound {
			t.Errorf("Reserve() kind = %q, want %q", got, errorx.KindNotFound)
		}
	})

	// The test this whole service is arranged around.
	t.Run("sells the last unit exactly once under concurrency", func(t *testing.T) {
		_, stock := setupStock(t)
		track(t, ctx, stock, "SHIRT-M", 1)

		const callers = 16

		var (
			wg        sync.WaitGroup
			mu        sync.Mutex
			succeeded int
			refused   int
		)

		wg.Add(callers)

		for range callers {
			go func() {
				defer wg.Done()

				err := stock.Reserve(ctx, "SHIRT-M", 1)

				mu.Lock()
				defer mu.Unlock()

				switch {
				case err == nil:
					succeeded++
				case errors.Is(err, domain.ErrInsufficientStock):
					refused++
				default:
					t.Errorf("Reserve() error = %v, want nil or insufficient stock", err)
				}
			}()
		}

		wg.Wait()

		// This is the guarantee, and it is the server's rather than this
		// process's: sixteen callers evaluated `available >= 1` against the row
		// as each of them found it, so fifteen of them found a zero the first
		// one had already written. A read followed by a write would have let
		// every one of them see the same 1.
		if succeeded != 1 {
			t.Errorf("%d callers reserved the last unit, want exactly 1", succeeded)
		}

		if refused != callers-1 {
			t.Errorf("%d callers were refused, want %d", refused, callers-1)
		}

		available, reserved := counts(t, ctx, stock, "SHIRT-M")
		if available != 0 || reserved != 1 {
			t.Errorf("counts = %d available, %d reserved, want 0 and 1", available, reserved)
		}
	})
}

func TestReleaseAndCommit(t *testing.T) {
	ctx := context.Background()

	t.Run("release puts the hold back", func(t *testing.T) {
		_, stock := setupStock(t)
		track(t, ctx, stock, "SHIRT-M", 5)

		if err := stock.Reserve(ctx, "SHIRT-M", 2); err != nil {
			t.Fatalf("Reserve() error = %v, want nil", err)
		}

		if err := stock.Release(ctx, "SHIRT-M", 2); err != nil {
			t.Fatalf("Release() error = %v, want nil", err)
		}

		available, reserved := counts(t, ctx, stock, "SHIRT-M")
		if available != 5 || reserved != 0 {
			t.Errorf("counts = %d available, %d reserved, want 5 and 0", available, reserved)
		}
	})

	t.Run("commit takes the goods away", func(t *testing.T) {
		_, stock := setupStock(t)
		track(t, ctx, stock, "SHIRT-M", 5)

		if err := stock.Reserve(ctx, "SHIRT-M", 2); err != nil {
			t.Fatalf("Reserve() error = %v, want nil", err)
		}

		if err := stock.Commit(ctx, "SHIRT-M", 2); err != nil {
			t.Fatalf("Commit() error = %v, want nil", err)
		}

		// The one movement that reduces what the warehouse holds: the hold is
		// gone and nothing came back to available.
		available, reserved := counts(t, ctx, stock, "SHIRT-M")
		if available != 3 || reserved != 0 {
			t.Errorf("counts = %d available, %d reserved, want 3 and 0", available, reserved)
		}
	})

	t.Run("releasing more than is held is this service's own bug", func(t *testing.T) {
		_, stock := setupStock(t)
		track(t, ctx, stock, "SHIRT-M", 5)

		// Nothing is reserved, so the guard refuses. Internal rather than a
		// conflict, because no caller can act on it: reaching it means the
		// counts and the reservations disagree.
		err := stock.Release(ctx, "SHIRT-M", 1)
		if got := errorx.KindOf(err); got != errorx.KindInternal {
			t.Fatalf("Release() kind = %q, want %q", got, errorx.KindInternal)
		}

		if got := errorx.Reason(err); got != "STOCK_INCONSISTENT" {
			t.Errorf("Release() reason = %q, want STOCK_INCONSISTENT", got)
		}
	})
}

func TestAdjust(t *testing.T) {
	ctx := context.Background()

	t.Run("a delivery and a breakage", func(t *testing.T) {
		_, stock := setupStock(t)
		track(t, ctx, stock, "SHIRT-M", 5)

		item, err := stock.Adjust(ctx, "SHIRT-M", 10)
		if err != nil {
			t.Fatalf("Adjust() error = %v, want nil", err)
		}

		if got := item.Available().Int32(); got != 15 {
			t.Errorf("Adjust() available = %d, want 15", got)
		}

		if item.Version() != 2 {
			t.Errorf("Adjust() version = %d, want 2", item.Version())
		}

		if _, err := stock.Adjust(ctx, "SHIRT-M", -3); err != nil {
			t.Fatalf("Adjust() error = %v, want nil", err)
		}

		if available, _ := counts(t, ctx, stock, "SHIRT-M"); available != 12 {
			t.Errorf("available = %d, want 12", available)
		}
	})

	t.Run("refuses to go below zero rather than clamping", func(t *testing.T) {
		_, stock := setupStock(t)
		track(t, ctx, stock, "SHIRT-M", 5)

		_, err := stock.Adjust(ctx, "SHIRT-M", -6)
		if got := errorx.KindOf(err); got != errorx.KindConflict {
			t.Fatalf("Adjust() kind = %q, want %q", got, errorx.KindConflict)
		}

		if got := errorx.Reason(err); got != "STOCK_WOULD_GO_NEGATIVE" {
			t.Errorf("Adjust() reason = %q, want STOCK_WOULD_GO_NEGATIVE", got)
		}

		// The count is wrong either way; clamping would hide which.
		if available, _ := counts(t, ctx, stock, "SHIRT-M"); available != 5 {
			t.Errorf("available = %d after a refusal, want 5", available)
		}
	})

	t.Run("an untracked sku is not found", func(t *testing.T) {
		_, stock := setupStock(t)

		_, err := stock.Adjust(ctx, "NEVER-HEARD-OF", 1)
		if got := errorx.KindOf(err); got != errorx.KindNotFound {
			t.Fatalf("Adjust() kind = %q, want %q", got, errorx.KindNotFound)
		}
	})
}
