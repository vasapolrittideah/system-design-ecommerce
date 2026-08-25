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

func placedEvent() in.OrderPlacedEvent {
	return in.OrderPlacedEvent{
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

	if err := saga.CommitReservation(context.Background(), placedEvent()); err != nil {
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

	if err := saga.CommitReservation(context.Background(), placedEvent()); err != nil {
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

	err := saga.CommitReservation(context.Background(), placedEvent())
	if !errors.Is(err, wantErr) {
		t.Fatalf("CommitReservation() error = %v, want %v", err, wantErr)
	}
}

func TestCommitReservationRejectsAMissingEventID(t *testing.T) {
	_, _, _, saga := sagaSetup(t)

	event := placedEvent()
	event.EventID = ""

	err := saga.CommitReservation(context.Background(), event)
	if errorx.KindOf(err) != errorx.KindInvalidInput {
		t.Fatalf("CommitReservation() error kind = %v, want %v", errorx.KindOf(err), errorx.KindInvalidInput)
	}
}

func TestCommitReservationRejectsAMalformedReservationID(t *testing.T) {
	_, _, _, saga := sagaSetup(t)

	event := placedEvent()
	event.ReservationID = "not-a-uuid"

	if err := saga.CommitReservation(context.Background(), event); err == nil {
		t.Fatal("CommitReservation() error = nil, want a rejection")
	}
}
