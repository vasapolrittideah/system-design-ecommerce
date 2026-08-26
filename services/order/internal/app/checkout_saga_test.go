package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/app"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out/mocks"
)

const (
	eventID   = "9c1a2b3d-4e5f-4061-8a2b-3c4d5e6f7a8b"
	orderID   = "3f5b8e2a-1c9d-4a6e-b3f0-2d7c9e4a1b5f"
	paymentID = "5d7c9e4a-1b5f-4a6e-b3f0-2d7c9e4a1b5f"
)

func sagaSetup(t *testing.T) (
	*mocks.MockOrderRepository,
	*mocks.MockInboxStore,
	*mocks.MockInventoryGateway,
	*mocks.MockTxManager,
	*app.CheckoutSagaService,
) {
	t.Helper()

	orders := mocks.NewMockOrderRepository(t)
	inbox := mocks.NewMockInboxStore(t)
	inventory := mocks.NewMockInventoryGateway(t)
	tx := mocks.NewMockTxManager(t)

	return orders, inbox, inventory, tx, app.NewCheckoutSagaService(orders, inbox, inventory, tx)
}

func paidEvent() in.OrderPaidEvent {
	return in.OrderPaidEvent{
		EventID:       eventID,
		OrderID:       orderID,
		ReservationID: reservationID,
	}
}

func cancelledEvent() in.OrderCancelledEvent {
	return in.OrderCancelledEvent{
		EventID:       eventID,
		OrderID:       orderID,
		ReservationID: reservationID,
	}
}

func TestCommitReservationClaimsThenCommits(t *testing.T) {
	_, inbox, inventory, tx, saga := sagaSetup(t)
	expectTx(tx)

	inbox.EXPECT().
		Claim(mock.Anything, "order.checkout-saga", eventID).
		Return(true, nil)
	inventory.EXPECT().
		Commit(mock.Anything, mock.Anything).
		Return(nil)

	if err := saga.CommitReservation(context.Background(), paidEvent()); err != nil {
		t.Fatalf("CommitReservation() error = %v, want nil", err)
	}
}

// A redelivery finds the event already claimed and must not call inventory a
// second time — the whole reason the claim runs first, inside the same
// transaction.
func TestCommitReservationSkipsAnAlreadyClaimedEvent(t *testing.T) {
	_, inbox, _, tx, saga := sagaSetup(t)
	expectTx(tx)

	inbox.EXPECT().
		Claim(mock.Anything, "order.checkout-saga", eventID).
		Return(false, nil)

	if err := saga.CommitReservation(context.Background(), paidEvent()); err != nil {
		t.Fatalf("CommitReservation() error = %v, want nil", err)
	}
	// No expectation was set on inventory, so mockery's own cleanup check is
	// what proves it was never called.
}

func TestCommitReservationRollsBackOnAClaimFailure(t *testing.T) {
	_, inbox, _, tx, saga := sagaSetup(t)
	expectTx(tx)

	wantErr := errors.New("boom")
	inbox.EXPECT().
		Claim(mock.Anything, "order.checkout-saga", eventID).
		Return(false, wantErr)

	err := saga.CommitReservation(context.Background(), paidEvent())
	if !errors.Is(err, wantErr) {
		t.Fatalf("CommitReservation() error = %v, want %v", err, wantErr)
	}
}

func TestCommitReservationRejectsAMissingEventID(t *testing.T) {
	_, _, _, _, saga := sagaSetup(t)

	event := paidEvent()
	event.EventID = ""

	err := saga.CommitReservation(context.Background(), event)
	if errorx.KindOf(err) != errorx.KindInvalidInput {
		t.Fatalf("CommitReservation() error kind = %v, want %v", errorx.KindOf(err), errorx.KindInvalidInput)
	}
}

func TestCommitReservationRejectsAMalformedReservationID(t *testing.T) {
	_, _, _, _, saga := sagaSetup(t)

	event := paidEvent()
	event.ReservationID = "not-a-uuid"

	if err := saga.CommitReservation(context.Background(), event); err == nil {
		t.Fatal("CommitReservation() error = nil, want a rejection")
	}
}

func TestReleaseReservationClaimsThenReleases(t *testing.T) {
	_, inbox, inventory, tx, saga := sagaSetup(t)
	expectTx(tx)

	inbox.EXPECT().
		Claim(mock.Anything, "order.checkout-saga", eventID).
		Return(true, nil)
	inventory.EXPECT().
		Release(mock.Anything, mock.Anything).
		Return(nil)

	if err := saga.ReleaseReservation(context.Background(), cancelledEvent()); err != nil {
		t.Fatalf("ReleaseReservation() error = %v, want nil", err)
	}
}

// The compensating step is redelivered like every other, and the claim is what
// stops it reaching inventory twice.
func TestReleaseReservationSkipsAnAlreadyClaimedEvent(t *testing.T) {
	_, inbox, _, tx, saga := sagaSetup(t)
	expectTx(tx)

	inbox.EXPECT().
		Claim(mock.Anything, "order.checkout-saga", eventID).
		Return(false, nil)

	if err := saga.ReleaseReservation(context.Background(), cancelledEvent()); err != nil {
		t.Fatalf("ReleaseReservation() error = %v, want nil", err)
	}
}

// A release that inventory refuses — the reservation is already committed —
// comes back rather than being swallowed, so kafkax retries and then dead-letters
// it. An order that was paid and then cancelled is a refund, which this service
// does not have.
func TestReleaseReservationPropagatesAnInventoryFailure(t *testing.T) {
	_, inbox, inventory, tx, saga := sagaSetup(t)
	expectTx(tx)

	wantErr := errors.New("already committed")
	inbox.EXPECT().
		Claim(mock.Anything, "order.checkout-saga", eventID).
		Return(true, nil)
	inventory.EXPECT().
		Release(mock.Anything, mock.Anything).
		Return(wantErr)

	err := saga.ReleaseReservation(context.Background(), cancelledEvent())
	if !errors.Is(err, wantErr) {
		t.Fatalf("ReleaseReservation() error = %v, want %v", err, wantErr)
	}
}

func TestReleaseReservationRejectsAMissingEventID(t *testing.T) {
	_, _, _, _, saga := sagaSetup(t)

	event := cancelledEvent()
	event.EventID = ""

	err := saga.ReleaseReservation(context.Background(), event)
	if errorx.KindOf(err) != errorx.KindInvalidInput {
		t.Fatalf("ReleaseReservation() error kind = %v, want %v", errorx.KindOf(err), errorx.KindInvalidInput)
	}
}

func TestReleaseReservationRejectsAMalformedReservationID(t *testing.T) {
	_, _, _, _, saga := sagaSetup(t)

	event := cancelledEvent()
	event.ReservationID = "not-a-uuid"

	if err := saga.ReleaseReservation(context.Background(), event); err == nil {
		t.Fatal("ReleaseReservation() error = nil, want a rejection")
	}
}

func paidEventFromPayment() in.PaymentSucceededEvent {
	return in.PaymentSucceededEvent{EventID: eventID, OrderID: orderID, PaymentID: paymentID}
}

// pendingOrder is an order as it is right after checkout, loaded back from
// storage — so it carries no events of its own and the transition under test is
// the only thing that raises one.
func pendingOrder(t *testing.T) *domain.Order {
	t.Helper()

	priced := domain.ReconstituteMoney(99800, "THB")

	return domain.ReconstituteOrder(domain.OrderSnapshot{
		ID:            domain.OrderID(orderID),
		UserID:        domain.UserID(userID),
		Status:        domain.StatusPendingPayment,
		Lines:         []domain.OrderLine{},
		Total:         priced,
		ReservationID: domain.ReservationID(reservationID),
		Version:       1,
	})
}

func TestMarkPaidClaimsThenMovesTheOrder(t *testing.T) {
	orders, inbox, _, tx, saga := sagaSetup(t)
	expectTx(tx)

	inbox.EXPECT().
		Claim(mock.Anything, "order.payment-saga", eventID).
		Return(true, nil)

	order := pendingOrder(t)
	orders.EXPECT().FindByIDForUpdate(mock.Anything, domain.OrderID(orderID)).Return(order, nil)
	orders.EXPECT().
		Update(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, o *domain.Order) (*domain.Order, error) {
			if o.Status() != domain.StatusPaid {
				t.Errorf("persisted status = %q, want %q", o.Status(), domain.StatusPaid)
			}

			return o, nil
		})

	if err := saga.MarkPaid(context.Background(), paidEventFromPayment()); err != nil {
		t.Fatalf("MarkPaid() error = %v, want nil", err)
	}
}

// This service never calls inventory on a payment event. Marking the order
// raises OrderPaid, and the commit that turns the hold into a sale is driven by
// that — which is what keeps the call as durable as the transition. The
// inventory mock has no expectations, so mockery proves it was untouched.
func TestMarkPaidDoesNotTouchInventory(t *testing.T) {
	orders, inbox, _, tx, saga := sagaSetup(t)
	expectTx(tx)

	inbox.EXPECT().Claim(mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	orders.EXPECT().FindByIDForUpdate(mock.Anything, mock.Anything).Return(pendingOrder(t), nil)
	orders.EXPECT().
		Update(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, o *domain.Order) (*domain.Order, error) { return o, nil })

	if err := saga.MarkPaid(context.Background(), paidEventFromPayment()); err != nil {
		t.Fatalf("MarkPaid() error = %v, want nil", err)
	}
}

// A redelivery finds the event already claimed and never loads the order at
// all: the repository has no expectations, which is what proves it.
func TestMarkPaidSkipsAnAlreadyClaimedEvent(t *testing.T) {
	_, inbox, _, tx, saga := sagaSetup(t)
	expectTx(tx)

	inbox.EXPECT().
		Claim(mock.Anything, "order.payment-saga", eventID).
		Return(false, nil)

	if err := saga.MarkPaid(context.Background(), paidEventFromPayment()); err != nil {
		t.Fatalf("MarkPaid() error = %v, want nil", err)
	}
}

// An order already paid moves nowhere, and nothing is written. The claim still
// commits, which is what it is for — the delivery is handled either way.
func TestMarkPaidWritesNothingForAnOrderAlreadyPaid(t *testing.T) {
	orders, inbox, _, tx, saga := sagaSetup(t)
	expectTx(tx)

	order := pendingOrder(t)
	if _, err := order.MarkPaid(); err != nil {
		t.Fatalf("MarkPaid() error = %v, want nil", err)
	}
	order.PullEvents()

	inbox.EXPECT().Claim(mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	orders.EXPECT().FindByIDForUpdate(mock.Anything, mock.Anything).Return(order, nil)

	if err := saga.MarkPaid(context.Background(), paidEventFromPayment()); err != nil {
		t.Fatalf("MarkPaid() error = %v, want nil", err)
	}
}

// Money taken for an order whose stock has been given back. It comes back as a
// conflict, is retried and then dead-lettered, and that is the right answer:
// there is nothing this service can do about it that a person should not do
// instead.
func TestMarkPaidRefusesACancelledOrder(t *testing.T) {
	orders, inbox, _, tx, saga := sagaSetup(t)
	expectTx(tx)

	order := pendingOrder(t)
	if _, err := order.Cancel(); err != nil {
		t.Fatalf("Cancel() error = %v, want nil", err)
	}
	order.PullEvents()

	inbox.EXPECT().Claim(mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	orders.EXPECT().FindByIDForUpdate(mock.Anything, mock.Anything).Return(order, nil)

	err := saga.MarkPaid(context.Background(), paidEventFromPayment())
	if !errors.Is(err, domain.ErrOrderCancelled) {
		t.Fatalf("MarkPaid() error = %v, want %v", err, domain.ErrOrderCancelled)
	}
}

// The two subscriptions claim under different groups, because the group is half
// the key in processed_events: one shared id would let a delivery from one
// topic mask a delivery from the other that happened to carry the same event id.
func TestThePaymentAndOrderSubscriptionsClaimUnderDifferentGroups(t *testing.T) {
	orders, inbox, inventory, tx, saga := sagaSetup(t)
	expectTx(tx)
	expectTx(tx)

	inbox.EXPECT().Claim(mock.Anything, "order.payment-saga", eventID).Return(true, nil)
	orders.EXPECT().FindByIDForUpdate(mock.Anything, mock.Anything).Return(pendingOrder(t), nil)
	orders.EXPECT().
		Update(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, o *domain.Order) (*domain.Order, error) { return o, nil })

	inbox.EXPECT().Claim(mock.Anything, "order.checkout-saga", eventID).Return(true, nil)
	inventory.EXPECT().Commit(mock.Anything, mock.Anything).Return(nil)

	if err := saga.MarkPaid(context.Background(), paidEventFromPayment()); err != nil {
		t.Fatalf("MarkPaid() error = %v, want nil", err)
	}
	if err := saga.CommitReservation(context.Background(), paidEvent()); err != nil {
		t.Fatalf("CommitReservation() error = %v, want nil", err)
	}
}

func TestMarkPaidRejectsAMissingEventID(t *testing.T) {
	_, _, _, _, saga := sagaSetup(t)
	ctx := context.Background()

	paid := paidEventFromPayment()
	paid.EventID = ""
	if errorx.KindOf(saga.MarkPaid(ctx, paid)) != errorx.KindInvalidInput {
		t.Error("MarkPaid() accepted an event with no id")
	}

}

func TestMarkPaidRejectsAMalformedOrderID(t *testing.T) {
	_, _, _, _, saga := sagaSetup(t)
	ctx := context.Background()

	paid := paidEventFromPayment()
	paid.OrderID = "not-a-uuid"
	if err := saga.MarkPaid(ctx, paid); err == nil {
		t.Error("MarkPaid() accepted a malformed order id")
	}

}
