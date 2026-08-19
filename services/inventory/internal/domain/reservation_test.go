package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
)

// The clock every test below judges expiry against. A fixed value rather than
// time.Now, so that a test asserting "this has expired" is not a test that
// passes because the suite was slow.
var now = time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)

const ttl = 15 * time.Minute

// orderID is a well-formed identifier the tests reuse. It is a constant rather
// than a fresh uuid because nothing here depends on two reservations having
// different orders.
const orderID = domain.OrderID("6f1c0a4e-2b8d-4c1a-9f3e-5d7b8a2c4e60")

func newReservation(t *testing.T, lines ...domain.Line) *domain.Reservation {
	t.Helper()

	reservation, err := domain.NewReservation(orderID, lines, now, ttl)
	if err != nil {
		t.Fatalf("NewReservation() error = %v, want nil", err)
	}

	return reservation
}

func line(sku string, quantity int32) domain.Line {
	return domain.Line{SKU: domain.SKU(sku), Quantity: domain.Quantity(quantity)}
}

func TestNewReservation(t *testing.T) {
	t.Run("holds what it was asked for", func(t *testing.T) {
		reservation := newReservation(t, line("SHIRT-M", 2))

		if got := reservation.Status(); got != domain.StatusHeld {
			t.Errorf("Status() = %q, want %q", got, domain.StatusHeld)
		}

		if got := reservation.ExpiresAt(); !got.Equal(now.Add(ttl)) {
			t.Errorf("ExpiresAt() = %v, want %v", got, now.Add(ttl))
		}

		if got := reservation.Version(); got != 1 {
			t.Errorf("Version() = %d, want 1", got)
		}
	})

	t.Run("sums repeats of one sku", func(t *testing.T) {
		reservation := newReservation(t, line("SHIRT-M", 2), line("SHIRT-M", 3))

		lines := reservation.Lines()
		if len(lines) != 1 {
			t.Fatalf("Lines() = %d entries, want 1", len(lines))
		}

		if got := lines[0].Quantity; got != 5 {
			t.Errorf("Lines()[0].Quantity = %d, want 5 — a cart that added the same shirt twice wants both", got)
		}
	})

	t.Run("sorts lines by sku", func(t *testing.T) {
		// The order the caller sent is deliberately the reverse of the answer:
		// reserving takes a row lock per SKU in this order, and two baskets
		// sharing SKUs deadlock unless everyone agrees on one sequence.
		reservation := newReservation(t, line("SHOE-42", 1), line("HAT-L", 1), line("SHIRT-M", 1))

		want := []domain.SKU{"HAT-L", "SHIRT-M", "SHOE-42"}
		for i, l := range reservation.Lines() {
			if l.SKU != want[i] {
				t.Errorf("Lines()[%d].SKU = %q, want %q", i, l.SKU, want[i])
			}
		}
	})

	t.Run("refuses what it cannot hold", func(t *testing.T) {
		tests := []struct {
			name  string
			lines []domain.Line
			ttl   time.Duration
		}{
			{name: "no lines", lines: nil, ttl: ttl},
			{name: "zero quantity", lines: []domain.Line{line("SHIRT-M", 0)}, ttl: ttl},
			{name: "negative quantity", lines: []domain.Line{line("SHIRT-M", -1)}, ttl: ttl},
			{name: "missing sku", lines: []domain.Line{line("", 1)}, ttl: ttl},
			{name: "over the per-sku ceiling", lines: []domain.Line{line("SHIRT-M", 10_001)}, ttl: ttl},
			{
				name:  "over the ceiling once summed",
				lines: []domain.Line{line("SHIRT-M", 9_000), line("SHIRT-M", 2_000)},
				ttl:   ttl,
			},
			{name: "no ttl", lines: []domain.Line{line("SHIRT-M", 1)}, ttl: 0},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if _, err := domain.NewReservation(orderID, tt.lines, now, tt.ttl); err == nil {
					t.Fatal("NewReservation() error = nil, want a validation error")
				}
			})
		}
	})

	t.Run("refuses more lines than it will hold", func(t *testing.T) {
		lines := make([]domain.Line, 0, 101)
		for i := range 101 {
			lines = append(lines, domain.Line{SKU: domain.SKU(string(rune('A'+i%26)) + "-SKU"), Quantity: 1})
		}

		if _, err := domain.NewReservation(orderID, lines, now, ttl); err == nil {
			t.Fatal("NewReservation() error = nil, want a validation error")
		}
	})
}

func TestCommit(t *testing.T) {
	t.Run("moves a live hold once", func(t *testing.T) {
		reservation := newReservation(t, line("SHIRT-M", 1))

		moved, err := reservation.Commit(now)
		if err != nil || !moved {
			t.Fatalf("Commit() = %v, %v, want true, nil", moved, err)
		}

		if got := reservation.Status(); got != domain.StatusCommitted {
			t.Errorf("Status() = %q, want %q", got, domain.StatusCommitted)
		}

		// The second call is what a retried saga step does, and reporting false
		// is what stops the stock being taken down twice.
		moved, err = reservation.Commit(now)
		if err != nil || moved {
			t.Fatalf("Commit() again = %v, %v, want false, nil", moved, err)
		}
	})

	t.Run("refuses a hold whose time has run out", func(t *testing.T) {
		reservation := newReservation(t, line("SHIRT-M", 1))

		// Exactly at the boundary, which is where the reaper's own predicate
		// already selects the row: the two have to agree or a commit succeeds
		// against capacity the sweep is in the middle of returning.
		if _, err := reservation.Commit(now.Add(ttl)); !errors.Is(err, domain.ErrReservationExpired) {
			t.Fatalf("Commit() error = %v, want %v", err, domain.ErrReservationExpired)
		}

		if got := reservation.Status(); got != domain.StatusHeld {
			t.Errorf("Status() = %q, want it left alone at %q", got, domain.StatusHeld)
		}
	})

	t.Run("refuses a hold that was given back", func(t *testing.T) {
		reservation := newReservation(t, line("SHIRT-M", 1))

		if _, err := reservation.Release(); err != nil {
			t.Fatalf("Release() error = %v, want nil", err)
		}

		if _, err := reservation.Commit(now); !errors.Is(err, domain.ErrReservationReleased) {
			t.Fatalf("Commit() error = %v, want %v", err, domain.ErrReservationReleased)
		}
	})
}

func TestRelease(t *testing.T) {
	t.Run("moves a live hold once", func(t *testing.T) {
		reservation := newReservation(t, line("SHIRT-M", 1))

		moved, err := reservation.Release()
		if err != nil || !moved {
			t.Fatalf("Release() = %v, %v, want true, nil", moved, err)
		}

		moved, err = reservation.Release()
		if err != nil || moved {
			t.Fatalf("Release() again = %v, %v, want false, nil — compensation runs more than once", moved, err)
		}
	})

	t.Run("is satisfied by a hold the reaper already swept", func(t *testing.T) {
		reservation := newReservation(t, line("SHIRT-M", 1))

		if _, err := reservation.Expire(now.Add(ttl)); err != nil {
			t.Fatalf("Expire() error = %v, want nil", err)
		}

		moved, err := reservation.Release()
		if err != nil || moved {
			t.Fatalf("Release() = %v, %v, want false, nil — the capacity is already back", moved, err)
		}
	})

	t.Run("refuses a sale", func(t *testing.T) {
		reservation := newReservation(t, line("SHIRT-M", 1))

		if _, err := reservation.Commit(now); err != nil {
			t.Fatalf("Commit() error = %v, want nil", err)
		}

		if _, err := reservation.Release(); !errors.Is(err, domain.ErrReservationCommitted) {
			t.Fatalf("Release() error = %v, want %v", err, domain.ErrReservationCommitted)
		}
	})
}

func TestExpire(t *testing.T) {
	t.Run("sweeps a hold whose time has run out", func(t *testing.T) {
		reservation := newReservation(t, line("SHIRT-M", 1))

		moved, err := reservation.Expire(now.Add(ttl + time.Second))
		if err != nil || !moved {
			t.Fatalf("Expire() = %v, %v, want true, nil", moved, err)
		}

		if got := reservation.Status(); got != domain.StatusExpired {
			t.Errorf("Status() = %q, want %q — a stranded saga must not look like a compensation", got, domain.StatusExpired)
		}
	})

	t.Run("refuses a hold that still has time", func(t *testing.T) {
		reservation := newReservation(t, line("SHIRT-M", 1))

		if _, err := reservation.Expire(now); !errors.Is(err, domain.ErrReservationNotExpired) {
			t.Fatalf("Expire() error = %v, want %v", err, domain.ErrReservationNotExpired)
		}
	})

	t.Run("leaves a finished reservation alone", func(t *testing.T) {
		reservation := newReservation(t, line("SHIRT-M", 1))

		if _, err := reservation.Commit(now); err != nil {
			t.Fatalf("Commit() error = %v, want nil", err)
		}

		moved, err := reservation.Expire(now.Add(ttl))
		if err != nil || moved {
			t.Fatalf("Expire() = %v, %v, want false, nil — a sale is not swept", moved, err)
		}
	})
}

func TestMatches(t *testing.T) {
	reservation := newReservation(t, line("SHOE-42", 1), line("SHIRT-M", 2))

	t.Run("the same basket in another order", func(t *testing.T) {
		other := newReservation(t, line("SHIRT-M", 2), line("SHOE-42", 1))

		if !reservation.Matches(other.Lines()) {
			t.Error("Matches() = false, want true — a retry sends the same basket however it was typed")
		}
	})

	t.Run("a different quantity", func(t *testing.T) {
		other := newReservation(t, line("SHIRT-M", 3), line("SHOE-42", 1))

		if reservation.Matches(other.Lines()) {
			t.Error("Matches() = true, want false")
		}
	})

	t.Run("a different sku", func(t *testing.T) {
		other := newReservation(t, line("HAT-L", 2), line("SHOE-42", 1))

		if reservation.Matches(other.Lines()) {
			t.Error("Matches() = true, want false")
		}
	})
}

func TestLinesAreACopy(t *testing.T) {
	reservation := newReservation(t, line("SHIRT-M", 2))

	reservation.Lines()[0].Quantity = 99

	if got := reservation.Lines()[0].Quantity; got != 2 {
		t.Errorf("Lines()[0].Quantity = %d after the caller wrote to it, want 2", got)
	}
}

func TestReconstituteValidatesNothing(t *testing.T) {
	// A hold taken before the per-SKU ceiling existed. Refusing to load it would
	// make the reservation unreadable and its stock unreturnable.
	reservation := domain.ReconstituteReservation(domain.ReservationSnapshot{
		ID:        domain.NewReservationID(),
		OrderID:   orderID,
		Lines:     []domain.Line{line("SHIRT-M", 50_000)},
		Status:    domain.StatusHeld,
		ExpiresAt: now.Add(ttl),
		Version:   3,
	})

	if got := reservation.Lines()[0].Quantity; got != 50_000 {
		t.Errorf("Lines()[0].Quantity = %d, want the stored 50000", got)
	}

	if _, err := reservation.Release(); err != nil {
		t.Errorf("Release() error = %v, want nil — an old hold still has to be returnable", err)
	}
}
