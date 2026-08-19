package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/app"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/port/out/mocks"
)

const (
	orderID = "6f1c0a4e-2b8d-4c1a-9f3e-5d7b8a2c4e60"
	ttl     = 15 * time.Minute
)

// A fixed clock, so that a test about expiry is not a test about how long the
// suite took to get here.
var now = time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)

// The mocks assert their own expectations on cleanup, so a call that was set up
// and never made fails the test — which is how "the stock was never touched"
// below is checked without asserting on a counter.
func setup(t *testing.T) (
	*mocks.MockStockRepository,
	*mocks.MockReservationRepository,
	*mocks.MockTxManager,
	*app.InventoryService,
) {
	t.Helper()

	stock := mocks.NewMockStockRepository(t)
	reservations := mocks.NewMockReservationRepository(t)
	tx := mocks.NewMockTxManager(t)

	service := app.NewInventoryService(stock, reservations, tx, app.Policy{
		ReservationTTL: ttl,
		Now:            func() time.Time { return now },
	})

	return stock, reservations, tx, service
}

// expectTx runs the use case's transactional work inline, so the assertions
// below are about what happened inside one and not about txmanager.
func expectTx(tx *mocks.MockTxManager) {
	tx.EXPECT().
		Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).
		Once()
}

// noHold is what the repository answers for an order that holds nothing, and it
// is the sentinel rather than a bare error because the use case tells it from a
// failed read with errors.Is.
func noHold() error {
	return errorx.Wrap(domain.ErrReservationNotFound, errorx.KindNotFound, "order holds no reservation")
}

func reserveCommand(lines ...in.NewLine) in.ReserveStockCommand {
	return in.ReserveStockCommand{OrderID: orderID, Lines: lines}
}

func heldReservation(t *testing.T, lines ...domain.Line) *domain.Reservation {
	t.Helper()

	reservation, err := domain.NewReservation(domain.OrderID(orderID), lines, now, ttl)
	if err != nil {
		t.Fatalf("NewReservation() error = %v, want nil", err)
	}

	return reservation
}

func TestReserveStock(t *testing.T) {
	t.Run("takes the stock in sku order and persists the hold", func(t *testing.T) {
		stock, reservations, tx, service := setup(t)
		expectTx(tx)

		reservations.EXPECT().FindHeldByOrderID(mock.Anything, domain.OrderID(orderID)).Return(nil, noHold()).Once()

		// The order matters and is not the order the caller sent: reserving
		// takes a row lock per SKU, so two baskets sharing SKUs deadlock unless
		// everyone takes them in one sequence.
		var taken []domain.SKU

		stock.EXPECT().
			Reserve(mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, sku domain.SKU, _ domain.Quantity) error {
				taken = append(taken, sku)

				return nil
			}).
			Times(2)

		reservations.EXPECT().
			Create(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, r *domain.Reservation) (*domain.Reservation, error) {
				if got := r.Status(); got != domain.StatusHeld {
					t.Errorf("Create() got status %q, want %q", got, domain.StatusHeld)
				}

				if got := r.ExpiresAt(); !got.Equal(now.Add(ttl)) {
					t.Errorf("Create() got expires_at %v, want %v", got, now.Add(ttl))
				}

				return r, nil
			}).
			Once()

		_, err := service.ReserveStock(t.Context(), reserveCommand(
			in.NewLine{SKU: "SHOE-42", Quantity: 1},
			in.NewLine{SKU: "SHIRT-M", Quantity: 2},
		))
		if err != nil {
			t.Fatalf("ReserveStock() error = %v, want nil", err)
		}

		if len(taken) != 2 || taken[0] != "SHIRT-M" || taken[1] != "SHOE-42" {
			t.Errorf("Reserve() called for %v, want the sku-sorted order [SHIRT-M SHOE-42]", taken)
		}
	})

	t.Run("returns the hold an order already has without taking more", func(t *testing.T) {
		_, reservations, tx, service := setup(t)
		expectTx(tx)

		existing := heldReservation(t, domain.Line{SKU: "SHIRT-M", Quantity: 2})

		reservations.EXPECT().FindHeldByOrderID(mock.Anything, domain.OrderID(orderID)).Return(existing, nil).Once()

		// No Reserve and no Create are set up. The mocks fail the test if either
		// is called, which is the whole assertion: a retried saga step must not
		// take a second hold.
		got, err := service.ReserveStock(t.Context(), reserveCommand(in.NewLine{SKU: "SHIRT-M", Quantity: 2}))
		if err != nil {
			t.Fatalf("ReserveStock() error = %v, want nil", err)
		}

		if got.ID() != existing.ID() {
			t.Errorf("ReserveStock() = %q, want the existing hold %q", got.ID(), existing.ID())
		}
	})

	t.Run("refuses a different basket under an order that already holds one", func(t *testing.T) {
		_, reservations, tx, service := setup(t)
		expectTx(tx)

		existing := heldReservation(t, domain.Line{SKU: "SHIRT-M", Quantity: 2})

		reservations.EXPECT().FindHeldByOrderID(mock.Anything, domain.OrderID(orderID)).Return(existing, nil).Once()

		_, err := service.ReserveStock(t.Context(), reserveCommand(in.NewLine{SKU: "SHIRT-M", Quantity: 5}))
		if got := errorx.KindOf(err); got != errorx.KindConflict {
			t.Fatalf("ReserveStock() kind = %q, want %q", got, errorx.KindConflict)
		}

		if got := errorx.Reason(err); got != "RESERVATION_LINES_DIFFER" {
			t.Errorf("ReserveStock() reason = %q, want RESERVATION_LINES_DIFFER", got)
		}
	})

	t.Run("stops at the first line it cannot fill", func(t *testing.T) {
		stock, reservations, tx, service := setup(t)
		expectTx(tx)

		reservations.EXPECT().FindHeldByOrderID(mock.Anything, domain.OrderID(orderID)).Return(nil, noHold()).Once()

		stock.EXPECT().Reserve(mock.Anything, domain.SKU("SHIRT-M"), domain.Quantity(2)).Return(nil).Once()
		stock.EXPECT().
			Reserve(mock.Anything, domain.SKU("SHOE-42"), domain.Quantity(1)).
			Return(errorx.Wrap(domain.ErrInsufficientStock, errorx.KindConflict, "out of stock").
				WithReason("OUT_OF_STOCK")).
			Once()

		// Create is deliberately not expected. The transaction rolls back, so
		// the SHIRT-M decrement is undone with it — which is the only reason
		// taking stock line by line is safe at all.
		_, err := service.ReserveStock(t.Context(), reserveCommand(
			in.NewLine{SKU: "SHIRT-M", Quantity: 2},
			in.NewLine{SKU: "SHOE-42", Quantity: 1},
		))
		if !errors.Is(err, domain.ErrInsufficientStock) {
			t.Fatalf("ReserveStock() error = %v, want %v", err, domain.ErrInsufficientStock)
		}
	})

	t.Run("refuses what the caller got wrong before opening a transaction", func(t *testing.T) {
		tests := []struct {
			name string
			cmd  in.ReserveStockCommand
		}{
			{name: "no order", cmd: in.ReserveStockCommand{OrderID: "not-a-uuid"}},
			{name: "no lines", cmd: reserveCommand()},
			{name: "a malformed sku", cmd: reserveCommand(in.NewLine{SKU: "a b", Quantity: 1})},
			{name: "nothing to hold", cmd: reserveCommand(in.NewLine{SKU: "SHIRT-M", Quantity: 0})},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// No expectTx: a malformed basket must not cost a BEGIN, and the
				// mock fails the test if one is opened.
				_, _, _, service := setup(t)

				if _, err := service.ReserveStock(t.Context(), tt.cmd); err == nil {
					t.Fatal("ReserveStock() error = nil, want a validation error")
				}
			})
		}
	})
}

func TestCommitReservation(t *testing.T) {
	t.Run("takes the held stock down and writes the aggregate back", func(t *testing.T) {
		stock, reservations, tx, service := setup(t)
		expectTx(tx)

		held := heldReservation(t, domain.Line{SKU: "SHIRT-M", Quantity: 2})

		reservations.EXPECT().FindByID(mock.Anything, held.ID()).Return(held, nil).Once()
		stock.EXPECT().Commit(mock.Anything, domain.SKU("SHIRT-M"), domain.Quantity(2)).Return(nil).Once()
		reservations.EXPECT().Update(mock.Anything, held).Return(held, nil).Once()

		if _, err := service.CommitReservation(t.Context(), held.ID().String()); err != nil {
			t.Fatalf("CommitReservation() error = %v, want nil", err)
		}
	})

	t.Run("moves nothing for a reservation that is already committed", func(t *testing.T) {
		_, reservations, tx, service := setup(t)
		expectTx(tx)

		held := heldReservation(t, domain.Line{SKU: "SHIRT-M", Quantity: 2})
		if _, err := held.Commit(now); err != nil {
			t.Fatalf("Commit() error = %v, want nil", err)
		}

		reservations.EXPECT().FindByID(mock.Anything, held.ID()).Return(held, nil).Once()

		// Neither stock.Commit nor reservations.Update is set up: a retried saga
		// step that took the goods down twice would sell them twice.
		if _, err := service.CommitReservation(t.Context(), held.ID().String()); err != nil {
			t.Fatalf("CommitReservation() error = %v, want nil", err)
		}
	})

	t.Run("refuses a hold whose time has run out", func(t *testing.T) {
		_, reservations, tx, service := setup(t)
		expectTx(tx)

		// Taken a full TTL ago, so it is expired against the service's clock
		// even though no reaper has been near it.
		expired := domain.ReconstituteReservation(domain.ReservationSnapshot{
			ID:        domain.NewReservationID(),
			OrderID:   domain.OrderID(orderID),
			Lines:     []domain.Line{{SKU: "SHIRT-M", Quantity: 2}},
			Status:    domain.StatusHeld,
			ExpiresAt: now.Add(-time.Minute),
			Version:   1,
		})

		reservations.EXPECT().FindByID(mock.Anything, expired.ID()).Return(expired, nil).Once()

		_, err := service.CommitReservation(t.Context(), expired.ID().String())
		if !errors.Is(err, domain.ErrReservationExpired) {
			t.Fatalf("CommitReservation() error = %v, want %v", err, domain.ErrReservationExpired)
		}
	})
}

func TestReleaseReservation(t *testing.T) {
	t.Run("gives the held stock back", func(t *testing.T) {
		stock, reservations, tx, service := setup(t)
		expectTx(tx)

		held := heldReservation(t, domain.Line{SKU: "SHIRT-M", Quantity: 2})

		reservations.EXPECT().FindByID(mock.Anything, held.ID()).Return(held, nil).Once()
		stock.EXPECT().Release(mock.Anything, domain.SKU("SHIRT-M"), domain.Quantity(2)).Return(nil).Once()
		reservations.EXPECT().Update(mock.Anything, held).Return(held, nil).Once()

		if _, err := service.ReleaseReservation(t.Context(), held.ID().String()); err != nil {
			t.Fatalf("ReleaseReservation() error = %v, want nil", err)
		}
	})

	t.Run("moves nothing for a reservation the reaper already swept", func(t *testing.T) {
		_, reservations, tx, service := setup(t)
		expectTx(tx)

		swept := domain.ReconstituteReservation(domain.ReservationSnapshot{
			ID:        domain.NewReservationID(),
			OrderID:   domain.OrderID(orderID),
			Lines:     []domain.Line{{SKU: "SHIRT-M", Quantity: 2}},
			Status:    domain.StatusExpired,
			ExpiresAt: now.Add(-time.Minute),
			Version:   2,
		})

		reservations.EXPECT().FindByID(mock.Anything, swept.ID()).Return(swept, nil).Once()

		if _, err := service.ReleaseReservation(t.Context(), swept.ID().String()); err != nil {
			t.Fatalf("ReleaseReservation() error = %v, want nil — the capacity is already back", err)
		}
	})
}

func TestExpireReservations(t *testing.T) {
	t.Run("returns the stock of every hold it claimed", func(t *testing.T) {
		stock, reservations, tx, service := setup(t)
		expectTx(tx)

		stale := domain.ReconstituteReservation(domain.ReservationSnapshot{
			ID:        domain.NewReservationID(),
			OrderID:   domain.OrderID(orderID),
			Lines:     []domain.Line{{SKU: "SHIRT-M", Quantity: 2}, {SKU: "SHOE-42", Quantity: 1}},
			Status:    domain.StatusHeld,
			ExpiresAt: now.Add(-time.Minute),
			Version:   1,
		})

		reservations.EXPECT().
			ClaimExpired(mock.Anything, now, 10).
			Return([]*domain.Reservation{stale}, nil).
			Once()

		stock.EXPECT().Release(mock.Anything, domain.SKU("SHIRT-M"), domain.Quantity(2)).Return(nil).Once()
		stock.EXPECT().Release(mock.Anything, domain.SKU("SHOE-42"), domain.Quantity(1)).Return(nil).Once()
		reservations.EXPECT().Update(mock.Anything, stale).Return(stale, nil).Once()

		swept, err := service.ExpireReservations(t.Context(), 10)
		if err != nil {
			t.Fatalf("ExpireReservations() error = %v, want nil", err)
		}

		if swept != 1 {
			t.Errorf("ExpireReservations() = %d, want 1", swept)
		}

		if got := stale.Status(); got != domain.StatusExpired {
			t.Errorf("Status() = %q, want %q", got, domain.StatusExpired)
		}
	})

	t.Run("reports nothing swept when the claim is empty", func(t *testing.T) {
		_, reservations, tx, service := setup(t)
		expectTx(tx)

		reservations.EXPECT().ClaimExpired(mock.Anything, now, 10).Return(nil, nil).Once()

		swept, err := service.ExpireReservations(t.Context(), 10)
		if err != nil || swept != 0 {
			t.Fatalf("ExpireReservations() = %d, %v, want 0, nil", swept, err)
		}
	})
}

func TestGetStockBySKUs(t *testing.T) {
	t.Run("normalises before it reads", func(t *testing.T) {
		stock, _, _, service := setup(t)

		stock.EXPECT().
			FindBySKUs(mock.Anything, []domain.SKU{"SHIRT-M", "SHOE-42"}).
			Return(nil, nil).
			Once()

		if _, err := service.GetStockBySKUs(t.Context(), []string{"shirt-m", " SHOE-42 "}); err != nil {
			t.Fatalf("GetStockBySKUs() error = %v, want nil", err)
		}
	})

	t.Run("one malformed sku fails the call", func(t *testing.T) {
		// A missing SKU is the ordinary case and comes back as fewer rows; a
		// malformed one is a bad request, and answering the rest would hide it.
		_, _, _, service := setup(t)

		if _, err := service.GetStockBySKUs(t.Context(), []string{"SHIRT-M", "a b"}); err == nil {
			t.Fatal("GetStockBySKUs() error = nil, want a validation error")
		}
	})
}
