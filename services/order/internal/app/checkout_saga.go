package app

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out"
)

// The groups this service claims deliveries under in its own processed_events
// table.
//
// Two, because they are two subscriptions: the same event id could in
// principle be issued by two different services, and a single group would let
// one service's delivery mask the other's. They match the KAFKA_CONSUMER_*
// group ids the worker is configured with, which is the only place the
// pairing is stated.
const (
	checkoutSagaConsumerGroup = "order.checkout-saga"
	paymentSagaConsumerGroup  = "order.payment-saga"
)

// CheckoutSagaService drives the half of checkout that runs after an outcome is
// persisted, over the ports it needs and none of the ones Checkout itself does.
type CheckoutSagaService struct {
	orders    out.OrderRepository
	inbox     out.InboxStore
	inventory out.InventoryGateway
	tx        out.TxManager
}

// Compile-time proof that the driving port is satisfied.
var _ in.CheckoutSaga = (*CheckoutSagaService)(nil)

// NewCheckoutSagaService wires the saga's continuation to its driven ports.
func NewCheckoutSagaService(
	orders out.OrderRepository,
	inbox out.InboxStore,
	inventory out.InventoryGateway,
	tx out.TxManager,
) *CheckoutSagaService {
	return &CheckoutSagaService{orders: orders, inbox: inbox, inventory: inventory, tx: tx}
}

// MarkPaid records against the order that the money is in.
//
// The claim, the transition, and the event the transition raises are one
// transaction. Anything less and an order can be marked paid without anything
// being told about it, which is the failure the outbox exists to remove.
//
// The order is loaded under a lock. The writers that meet here are ordinary
// rather than rare — a payment arriving and a saga timeout can both decide what
// becomes of one order — and Postgres ordering them is what makes the second
// read the first one's outcome instead of racing it to a version number.
//
// The transition is the aggregate's own method rather than a status written
// here. Whether a cancelled order may be paid is a rule about orders, and a use
// case that decided it would be a second copy of the state machine.
//
// A payment for an order that was already cancelled comes back as a conflict
// and is retried and then dead-lettered, which is the right answer: money was
// taken for an order whose stock has been given back, and there is nothing this
// service can do about that which a person should not do instead.
func (s *CheckoutSagaService) MarkPaid(ctx context.Context, event in.PaymentSucceededEvent) error {
	if event.EventID == "" {
		return errorx.New(errorx.KindInvalidInput, "event_id is required")
	}

	orderID, err := domain.ParseOrderID(event.OrderID)
	if err != nil {
		return err
	}

	return s.tx.Do(ctx, func(ctx context.Context) error {
		claimed, err := s.inbox.Claim(ctx, paymentSagaConsumerGroup, event.EventID)
		if err != nil || !claimed {
			return err
		}

		order, err := s.orders.FindByIDForUpdate(ctx, orderID)
		if err != nil {
			return err
		}

		moved, err := order.MarkPaid()
		if err != nil || !moved {
			// Not moved and no error is a payment already recorded. The claim
			// above still commits, which is what it is for: the delivery is
			// handled either way.
			return err
		}

		_, err = s.orders.Update(ctx, order)

		return err
	})
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

// claimThen is the shape both inventory continuations share: take the delivery
// in the inbox, then make the one call to inventory it triggers.
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
