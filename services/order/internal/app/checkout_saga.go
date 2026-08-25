package app

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out"
)

// checkoutSagaConsumerGroup is what this service claims its own order-event
// deliveries under in its own processed_events table.
const checkoutSagaConsumerGroup = "order.checkout-saga"

// CheckoutSagaService drives the half of checkout that runs after an order's
// outcome is persisted, over the two ports it needs and none of the ones
// Checkout itself does — it never touches an order row directly, only
// inventory's.
type CheckoutSagaService struct {
	inbox     out.InboxStore
	inventory out.InventoryGateway
	tx        out.TxManager
}

// Compile-time proof that the driving port is satisfied.
var _ in.CheckoutSaga = (*CheckoutSagaService)(nil)

// NewCheckoutSagaService wires the saga's continuation to its driven ports.
func NewCheckoutSagaService(inbox out.InboxStore, inventory out.InventoryGateway, tx out.TxManager) *CheckoutSagaService {
	return &CheckoutSagaService{inbox: inbox, inventory: inventory, tx: tx}
}

// CommitReservation turns the stock held for an order into a sale, on the
// strength of the money being in.
func (s *CheckoutSagaService) CommitReservation(ctx context.Context, event in.OrderPaidEvent) error {
	return s.claimThen(ctx, event.EventID, event.ReservationID, s.inventory.Commit)
}

// ReleaseReservation gives the stock held for an order back.
//
// It is the compensating step, and it is idempotent at the far end for the
// reason every compensation is: it runs more than once. Inventory refuses a
// reservation it has already committed, which is the failure that says a paid
// order was cancelled — that is a refund, and this service does not have one.
func (s *CheckoutSagaService) ReleaseReservation(ctx context.Context, event in.OrderCancelledEvent) error {
	return s.claimThen(ctx, event.EventID, event.ReservationID, s.inventory.Release)
}

// claimThen is the shape both continuations share: take the delivery in the
// inbox, then make the one call to inventory it triggers.
//
// The claim and the call run in one transaction, which is safe only because
// both calls are idempotent on inventory's own side: if the call succeeds but
// this transaction then fails to commit, redelivery claims the event again and
// calls inventory again, and the second call changes nothing.
func (s *CheckoutSagaService) claimThen(
	ctx context.Context,
	eventID, reservationID string,
	call func(context.Context, domain.ReservationID) error,
) error {
	if eventID == "" {
		return errorx.New(errorx.KindInvalidInput, "event_id is required")
	}

	parsed, err := domain.ParseReservationID(reservationID)
	if err != nil {
		return err
	}

	return s.tx.Do(ctx, func(ctx context.Context) error {
		claimed, err := s.inbox.Claim(ctx, checkoutSagaConsumerGroup, eventID)
		if err != nil || !claimed {
			return err
		}

		return call(ctx, parsed)
	})
}
