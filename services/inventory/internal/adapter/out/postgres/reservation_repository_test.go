package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres/postgrestest"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/adapter/out/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
)

const ttl = 15 * time.Minute

// now is fixed so that a test about expiry is about the predicate rather than
// about how long the suite took to get here. It is truncated to the microsecond
// PostgreSQL stores, so a timestamp compares equal after a round trip.
var now = time.Now().UTC().Truncate(time.Microsecond)

func setupReservations(t *testing.T) (*pgxpool.Pool, *adapter.ReservationRepository) {
	t.Helper()

	pool := postgrestest.New(t, upMigrations(t)...)

	return pool, adapter.NewReservationRepository(pool)
}

// newHold builds a reservation for a fresh order, taken at `at`.
func newHold(t *testing.T, at time.Time, lines ...domain.Line) *domain.Reservation {
	t.Helper()

	orderID, err := domain.ParseOrderID(uuid.NewString())
	if err != nil {
		t.Fatalf("ParseOrderID() error = %v, want nil", err)
	}

	reservation, err := domain.NewReservation(orderID, lines, at, ttl)
	if err != nil {
		t.Fatalf("NewReservation() error = %v, want nil", err)
	}

	return reservation
}

func line(sku string, quantity int32) domain.Line {
	return domain.Line{SKU: domain.SKU(sku), Quantity: domain.Quantity(quantity)}
}

func TestCreateAndFindByID(t *testing.T) {
	ctx := context.Background()
	_, reservations := setupReservations(t)

	created, err := reservations.Create(ctx, newHold(t, now, line("SHOE-42", 1), line("SHIRT-M", 2)))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	// The timestamps are the database's, and the aggregate had none before the
	// insert returned.
	if created.CreatedAt().IsZero() || created.UpdatedAt().IsZero() {
		t.Errorf("Create() timestamps = %v/%v, want the values DEFAULT now() supplied",
			created.CreatedAt(), created.UpdatedAt())
	}

	found, err := reservations.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}

	if got := found.Status(); got != domain.StatusHeld {
		t.Errorf("FindByID() status = %q, want %q", got, domain.StatusHeld)
	}

	if got := found.ExpiresAt(); !got.Equal(now.Add(ttl)) {
		t.Errorf("FindByID() expires_at = %v, want %v", got, now.Add(ttl))
	}

	// Sorted by SKU on the way in and on the way out, which is what makes the
	// order a reservation takes its row locks in the same every time.
	lines := found.Lines()
	if len(lines) != 2 || lines[0].SKU != "SHIRT-M" || lines[1].SKU != "SHOE-42" {
		t.Fatalf("FindByID() lines = %v, want SHIRT-M then SHOE-42", lines)
	}

	if lines[0].Quantity != 2 {
		t.Errorf("FindByID() lines[0].Quantity = %d, want 2", lines[0].Quantity)
	}
}

func TestFindByIDMissing(t *testing.T) {
	ctx := context.Background()
	_, reservations := setupReservations(t)

	_, err := reservations.FindByID(ctx, domain.NewReservationID())

	// The sentinel matters as much as the code: the reserving use case tells
	// "this order holds nothing yet" from "the read failed" with errors.Is.
	if !errors.Is(err, domain.ErrReservationNotFound) {
		t.Fatalf("FindByID() error = %v, want %v", err, domain.ErrReservationNotFound)
	}

	if got := errorx.KindOf(err); got != errorx.KindNotFound {
		t.Errorf("FindByID() kind = %q, want %q", got, errorx.KindNotFound)
	}
}

func TestOneHeldReservationPerOrder(t *testing.T) {
	ctx := context.Background()
	_, reservations := setupReservations(t)

	first := newHold(t, now, line("SHIRT-M", 2))
	if _, err := reservations.Create(ctx, first); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	t.Run("a second hold for the same order is refused", func(t *testing.T) {
		second := domain.ReconstituteReservation(domain.ReservationSnapshot{
			ID:        domain.NewReservationID(),
			OrderID:   first.OrderID(),
			Lines:     []domain.Line{line("SHIRT-M", 2)},
			Status:    domain.StatusHeld,
			ExpiresAt: now.Add(ttl),
			Version:   1,
		})

		_, err := reservations.Create(ctx, second)

		// A conflict rather than a 500: the winner's hold is real, so the answer
		// is to ask again rather than to give up.
		if got := errorx.KindOf(err); got != errorx.KindConflict {
			t.Fatalf("Create() kind = %q, want %q", got, errorx.KindConflict)
		}

		if got := errorx.Reason(err); got != "RESERVATION_IN_FLIGHT" {
			t.Errorf("Create() reason = %q, want RESERVATION_IN_FLIGHT", got)
		}
	})

	t.Run("the live hold is what a retry finds", func(t *testing.T) {
		found, err := reservations.FindHeldByOrderID(ctx, first.OrderID())
		if err != nil {
			t.Fatalf("FindHeldByOrderID() error = %v, want nil", err)
		}

		if found.ID() != first.ID() {
			t.Errorf("FindHeldByOrderID() = %q, want %q", found.ID(), first.ID())
		}
	})

	t.Run("a finished hold frees the order to reserve again", func(t *testing.T) {
		// The whole reason the index is partial. An order whose hold expired
		// before it was paid for has to be able to take another one, and a plain
		// UNIQUE on the column would leave it permanently unfillable.
		if _, err := first.Release(); err != nil {
			t.Fatalf("Release() error = %v, want nil", err)
		}

		if _, err := reservations.Update(ctx, first); err != nil {
			t.Fatalf("Update() error = %v, want nil", err)
		}

		if _, err := reservations.FindHeldByOrderID(ctx, first.OrderID()); !errors.Is(err, domain.ErrReservationNotFound) {
			t.Fatalf("FindHeldByOrderID() error = %v, want %v — a released hold is history", err, domain.ErrReservationNotFound)
		}

		again := domain.ReconstituteReservation(domain.ReservationSnapshot{
			ID:        domain.NewReservationID(),
			OrderID:   first.OrderID(),
			Lines:     []domain.Line{line("SHIRT-M", 2)},
			Status:    domain.StatusHeld,
			ExpiresAt: now.Add(ttl),
			Version:   1,
		})

		if _, err := reservations.Create(ctx, again); err != nil {
			t.Fatalf("Create() error = %v, want nil", err)
		}
	})
}

func TestUpdateHoldsTheOptimisticLock(t *testing.T) {
	ctx := context.Background()
	_, reservations := setupReservations(t)

	created, err := reservations.Create(ctx, newHold(t, now, line("SHIRT-M", 1)))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	// Two callers loaded version 1 — a commit and a release racing.
	committer, err := reservations.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}

	releaser, err := reservations.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}

	if _, err := committer.Commit(now); err != nil {
		t.Fatalf("Commit() error = %v, want nil", err)
	}

	updated, err := reservations.Update(ctx, committer)
	if err != nil {
		t.Fatalf("Update() error = %v, want nil", err)
	}

	if updated.Version() != 2 {
		t.Errorf("Update() version = %d, want 2", updated.Version())
	}

	if _, err := releaser.Release(); err != nil {
		t.Fatalf("Release() error = %v, want nil", err)
	}

	_, err = reservations.Update(ctx, releaser)

	// A conflict and not a not-found: the row is plainly there, and a client
	// told "not found" would stop retrying where the answer is to re-read.
	if got := errorx.KindOf(err); got != errorx.KindConflict {
		t.Fatalf("Update() kind = %q, want %q", got, errorx.KindConflict)
	}

	if got := errorx.Reason(err); got != "RESERVATION_MODIFIED" {
		t.Errorf("Update() reason = %q, want RESERVATION_MODIFIED", got)
	}

	// The sale stands. Without the lock the release would have quietly undone it.
	found, err := reservations.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}

	if got := found.Status(); got != domain.StatusCommitted {
		t.Errorf("status = %q, want %q", got, domain.StatusCommitted)
	}
}

func TestClaimExpired(t *testing.T) {
	ctx := context.Background()

	t.Run("takes only held reservations whose time has run out", func(t *testing.T) {
		_, reservations := setupReservations(t)

		stale := newHold(t, now.Add(-time.Hour), line("SHIRT-M", 1))
		if _, err := reservations.Create(ctx, stale); err != nil {
			t.Fatalf("Create() error = %v, want nil", err)
		}

		live := newHold(t, now, line("SHOE-42", 1))
		if _, err := reservations.Create(ctx, live); err != nil {
			t.Fatalf("Create() error = %v, want nil", err)
		}

		finished := newHold(t, now.Add(-time.Hour), line("HAT-L", 1))
		if _, err := reservations.Create(ctx, finished); err != nil {
			t.Fatalf("Create() error = %v, want nil", err)
		}

		if _, err := finished.Commit(now.Add(-time.Hour)); err != nil {
			t.Fatalf("Commit() error = %v, want nil", err)
		}

		if _, err := reservations.Update(ctx, finished); err != nil {
			t.Fatalf("Update() error = %v, want nil", err)
		}

		claimed, err := reservations.ClaimExpired(ctx, now, 10)
		if err != nil {
			t.Fatalf("ClaimExpired() error = %v, want nil", err)
		}

		if len(claimed) != 1 {
			t.Fatalf("ClaimExpired() = %d reservations, want 1", len(claimed))
		}

		if claimed[0].ID() != stale.ID() {
			t.Errorf("ClaimExpired() took %q, want the stale hold %q", claimed[0].ID(), stale.ID())
		}

		// The lines come with it, because releasing means giving back exactly
		// what was held.
		if got := claimed[0].Lines(); len(got) != 1 || got[0].SKU != "SHIRT-M" {
			t.Errorf("ClaimExpired() lines = %v, want one line for SHIRT-M", got)
		}
	})

	t.Run("two sweepers divide the backlog rather than fight over it", func(t *testing.T) {
		pool, reservations := setupReservations(t)

		for range 4 {
			if _, err := reservations.Create(ctx, newHold(t, now.Add(-time.Hour), line("SHIRT-M", 1))); err != nil {
				t.Fatalf("Create() error = %v, want nil", err)
			}
		}

		tx := txmanager.New(pool)

		var first, second []*domain.Reservation

		// The first sweeper claims two and holds its transaction open. SKIP
		// LOCKED is what lets the second one step over those rows instead of
		// blocking behind them, which is the difference between a rolling
		// restart briefly running two reapers and one of them stalling.
		err := tx.Do(ctx, func(ctx context.Context) error {
			var err error

			first, err = reservations.ClaimExpired(ctx, now, 2)
			if err != nil {
				return err
			}

			return tx.Do(context.Background(), func(other context.Context) error {
				second, err = reservations.ClaimExpired(other, now, 2)

				return err
			})
		})
		if err != nil {
			t.Fatalf("ClaimExpired() error = %v, want nil", err)
		}

		if len(first) != 2 || len(second) != 2 {
			t.Fatalf("claims = %d and %d, want 2 and 2", len(first), len(second))
		}

		taken := make(map[domain.ReservationID]bool, 4)
		for _, reservation := range append(first, second...) {
			if taken[reservation.ID()] {
				t.Errorf("reservation %q was claimed by both sweepers", reservation.ID())
			}

			taken[reservation.ID()] = true
		}
	})
}
