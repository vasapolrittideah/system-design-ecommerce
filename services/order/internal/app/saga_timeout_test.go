package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/app"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out/mocks"
)

const paymentWindow = 15 * time.Minute

// frozen is the clock this sweep judges by, so no test waits out a window.
var frozen = time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

func timeoutSetup(t *testing.T) (*mocks.MockOrderRepository, *mocks.MockTxManager, *app.SagaTimeoutService) {
	t.Helper()

	orders := mocks.NewMockOrderRepository(t)
	tx := mocks.NewMockTxManager(t)

	policy := app.Policy{PaymentWindow: paymentWindow, Now: func() time.Time { return frozen }}

	return orders, tx, app.NewSagaTimeoutService(orders, tx, policy)
}

// staleOrder is an order created long enough ago that its window has run out.
func staleOrder(t *testing.T, age time.Duration) *domain.Order {
	t.Helper()

	return domain.ReconstituteOrder(domain.OrderSnapshot{
		ID:            domain.NewOrderID(),
		UserID:        domain.UserID(userID),
		Status:        domain.StatusPendingPayment,
		Total:         domain.ReconstituteMoney(99800, "THB"),
		ReservationID: domain.ReservationID(reservationID),
		CreatedAt:     frozen.Add(-age),
		Version:       1,
	})
}

func TestExpireStaleOrdersCancelsWhatItClaimed(t *testing.T) {
	orders, tx, service := timeoutSetup(t)
	expectTx(tx)

	claimed := []*domain.Order{
		staleOrder(t, paymentWindow+time.Minute),
		staleOrder(t, 2*time.Hour),
	}

	// The cut-off is the moment the window closes for an order created now.
	orders.EXPECT().
		ClaimStaleOrders(mock.Anything, frozen.Add(-paymentWindow), 10).
		Return(claimed, nil)

	orders.EXPECT().
		Update(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, o *domain.Order) (*domain.Order, error) {
			if o.Status() != domain.StatusCancelled {
				t.Errorf("persisted status = %q, want %q", o.Status(), domain.StatusCancelled)
			}

			return o, nil
		}).
		Twice()

	expired, err := service.ExpireStaleOrders(context.Background(), 10)
	if err != nil {
		t.Fatalf("ExpireStaleOrders() error = %v, want nil", err)
	}
	if expired != 2 {
		t.Errorf("expired = %d, want 2", expired)
	}
}

// Cancelling raises OrderCancelled, and the reservation is given back by the
// subscription that reads it. This sweep calls nobody, which is why the service
// has no gateway to call — and what the repository is handed is an aggregate
// carrying the event.
func TestExpireStaleOrdersRaisesTheCompensatingFact(t *testing.T) {
	orders, tx, service := timeoutSetup(t)
	expectTx(tx)

	orders.EXPECT().
		ClaimStaleOrders(mock.Anything, mock.Anything, mock.Anything).
		Return([]*domain.Order{staleOrder(t, 2*time.Hour)}, nil)

	orders.EXPECT().
		Update(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, o *domain.Order) (*domain.Order, error) {
			pulled := o.PullEvents()
			if len(pulled) != 1 {
				t.Fatalf("the order carries %d events, want 1", len(pulled))
			}
			if _, ok := pulled[0].(domain.OrderCancelled); !ok {
				t.Errorf("event is %T, want domain.OrderCancelled", pulled[0])
			}

			return o, nil
		})

	if _, err := service.ExpireStaleOrders(context.Background(), 10); err != nil {
		t.Fatalf("ExpireStaleOrders() error = %v, want nil", err)
	}
}

// A payment that landed between the claim and the transition is a race this
// sweep should lose. The order is skipped and the rest of the batch still runs;
// the repository gets no Update for it, which mockery's cleanup proves.
func TestExpireStaleOrdersSkipsAnOrderPaidUnderIt(t *testing.T) {
	orders, tx, service := timeoutSetup(t)
	expectTx(tx)

	paid := staleOrder(t, 2*time.Hour)
	if _, err := paid.MarkPaid(); err != nil {
		t.Fatalf("MarkPaid() error = %v, want nil", err)
	}
	paid.PullEvents()

	stale := staleOrder(t, 2*time.Hour)

	orders.EXPECT().
		ClaimStaleOrders(mock.Anything, mock.Anything, mock.Anything).
		Return([]*domain.Order{paid, stale}, nil)

	orders.EXPECT().
		Update(mock.Anything, stale).
		RunAndReturn(func(_ context.Context, o *domain.Order) (*domain.Order, error) { return o, nil })

	expired, err := service.ExpireStaleOrders(context.Background(), 10)
	if err != nil {
		t.Fatalf("ExpireStaleOrders() error = %v, want nil", err)
	}
	if expired != 1 {
		t.Errorf("expired = %d, want 1 — the paid order is not this sweep's to end", expired)
	}
}

// An order the query selected but the aggregate does not think is due. The two
// predicates disagreeing is a bug worth not compounding, and skipping is what
// keeps the rest of the batch moving.
func TestExpireStaleOrdersSkipsAnOrderInsideItsWindow(t *testing.T) {
	orders, tx, service := timeoutSetup(t)
	expectTx(tx)

	orders.EXPECT().
		ClaimStaleOrders(mock.Anything, mock.Anything, mock.Anything).
		Return([]*domain.Order{staleOrder(t, time.Minute)}, nil)

	expired, err := service.ExpireStaleOrders(context.Background(), 10)
	if err != nil {
		t.Fatalf("ExpireStaleOrders() error = %v, want nil", err)
	}
	if expired != 0 {
		t.Errorf("expired = %d, want 0", expired)
	}
}

// A misconfigured window fails the batch rather than being skipped. Skipping it
// would leave the sweep reporting zero forever with nothing to say why.
func TestExpireStaleOrdersFailsOnAWindowThatIsNotOne(t *testing.T) {
	orders := mocks.NewMockOrderRepository(t)
	tx := mocks.NewMockTxManager(t)
	expectTx(tx)

	service := app.NewSagaTimeoutService(orders, tx,
		app.Policy{PaymentWindow: 0, Now: func() time.Time { return frozen }})

	orders.EXPECT().
		ClaimStaleOrders(mock.Anything, mock.Anything, mock.Anything).
		Return([]*domain.Order{staleOrder(t, 2*time.Hour)}, nil)

	if _, err := service.ExpireStaleOrders(context.Background(), 10); err == nil {
		t.Fatal("ExpireStaleOrders() error = nil, want the misconfiguration reported")
	}
}

func TestExpireStaleOrdersPropagatesAClaimFailure(t *testing.T) {
	orders, tx, service := timeoutSetup(t)
	expectTx(tx)

	wantErr := errors.New("boom")
	orders.EXPECT().
		ClaimStaleOrders(mock.Anything, mock.Anything, mock.Anything).
		Return(nil, wantErr)

	_, err := service.ExpireStaleOrders(context.Background(), 10)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ExpireStaleOrders() error = %v, want %v", err, wantErr)
	}
}

// Zero means the default rather than "every stale order in one transaction",
// which is a batch holding a row lock on the whole backlog.
func TestExpireStaleOrdersBoundsAnUnboundedLimit(t *testing.T) {
	orders, tx, service := timeoutSetup(t)
	expectTx(tx)

	orders.EXPECT().
		ClaimStaleOrders(mock.Anything, mock.Anything, 100).
		Return(nil, nil)

	if _, err := service.ExpireStaleOrders(context.Background(), 0); err != nil {
		t.Fatalf("ExpireStaleOrders() error = %v, want nil", err)
	}
}
