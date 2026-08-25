package app

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out"
)

// checkoutSagaConsumerGroup is what this service claims OrderPlaced
// deliveries under in its own processed_events table.
const checkoutSagaConsumerGroup = "order.checkout-saga"

// CheckoutSagaService drives the half of checkout that runs after the order
// is persisted, over the two ports it needs and none of the ones Checkout
// itself does — it never touches an order row directly, only inventory's.
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

// CommitReservation turns the stock held for an order into a sale.
//
// Claiming the event and calling inventory run in one transaction, which is
// safe only because CommitReservation is idempotent on inventory's own side:
// if the call succeeds but this transaction then fails to commit, redelivery
// claims the event again and calls inventory again, and the second call
// changes nothing.
func (s *CheckoutSagaService) CommitReservation(ctx context.Context, event in.OrderPlacedEvent) error {
	if event.EventID == "" {
		return errorx.New(errorx.KindInvalidInput, "event_id is required")
	}

	reservationID, err := domain.ParseReservationID(event.ReservationID)
	if err != nil {
		return err
	}

	return s.tx.Do(ctx, func(ctx context.Context) error {
		claimed, err := s.inbox.Claim(ctx, checkoutSagaConsumerGroup, event.EventID)
		if err != nil || !claimed {
			return err
		}

		return s.inventory.Commit(ctx, reservationID)
	})
}
