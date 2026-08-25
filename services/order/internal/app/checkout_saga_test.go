package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/app"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out/mocks"
)

const (
	eventID = "9c1a2b3d-4e5f-4061-8a2b-3c4d5e6f7a8b"
	orderID = "3f5b8e2a-1c9d-4a6e-b3f0-2d7c9e4a1b5f"
)

func sagaSetup(t *testing.T) (
	*mocks.MockInboxStore,
	*mocks.MockInventoryGateway,
	*mocks.MockTxManager,
	*app.CheckoutSagaService,
) {
	t.Helper()

	inbox := mocks.NewMockInboxStore(t)
	inventory := mocks.NewMockInventoryGateway(t)
	tx := mocks.NewMockTxManager(t)

	return inbox, inventory, tx, app.NewCheckoutSagaService(inbox, inventory, tx)
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
	inbox, inventory, tx, saga := sagaSetup(t)
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
	inbox, _, tx, saga := sagaSetup(t)
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
	inbox, _, tx, saga := sagaSetup(t)
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
	_, _, _, saga := sagaSetup(t)

	event := paidEvent()
	event.EventID = ""

	err := saga.CommitReservation(context.Background(), event)
	if errorx.KindOf(err) != errorx.KindInvalidInput {
		t.Fatalf("CommitReservation() error kind = %v, want %v", errorx.KindOf(err), errorx.KindInvalidInput)
	}
}

func TestCommitReservationRejectsAMalformedReservationID(t *testing.T) {
	_, _, _, saga := sagaSetup(t)

	event := paidEvent()
	event.ReservationID = "not-a-uuid"

	if err := saga.CommitReservation(context.Background(), event); err == nil {
		t.Fatal("CommitReservation() error = nil, want a rejection")
	}
}

func TestReleaseReservationClaimsThenReleases(t *testing.T) {
	inbox, inventory, tx, saga := sagaSetup(t)
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
	inbox, _, tx, saga := sagaSetup(t)
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
	inbox, inventory, tx, saga := sagaSetup(t)
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
	_, _, _, saga := sagaSetup(t)

	event := cancelledEvent()
	event.EventID = ""

	err := saga.ReleaseReservation(context.Background(), event)
	if errorx.KindOf(err) != errorx.KindInvalidInput {
		t.Fatalf("ReleaseReservation() error kind = %v, want %v", errorx.KindOf(err), errorx.KindInvalidInput)
	}
}

func TestReleaseReservationRejectsAMalformedReservationID(t *testing.T) {
	_, _, _, saga := sagaSetup(t)

	event := cancelledEvent()
	event.ReservationID = "not-a-uuid"

	if err := saga.ReleaseReservation(context.Background(), event); err == nil {
		t.Fatal("ReleaseReservation() error = nil, want a rejection")
	}
}
