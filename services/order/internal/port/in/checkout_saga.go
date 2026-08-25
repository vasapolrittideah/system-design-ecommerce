package in

import "context"

// OrderPaidEvent is what arrived on this service's own OrderPaid topic, as
// much of it as continuing the saga needs.
type OrderPaidEvent struct {
	// EventID is the envelope's id, claimed in the inbox so a redelivery does
	// not call inventory a second time.
	EventID string

	OrderID       string
	ReservationID string
}

// OrderCancelledEvent is the same for OrderCancelled, whose continuation is
// the compensating step rather than the forward one.
type OrderCancelledEvent struct {
	EventID string

	OrderID       string
	ReservationID string
}

// CheckoutSaga is what this service's own consumer of its order events drives:
// the half of checkout that runs after an order's outcome is durably
// persisted rather than inside the transaction that persisted it.
//
// Both methods make a call to inventory that cannot be made from inside that
// transaction — a call made right after commit is a write nothing would retry
// if the process died between the two — so the trigger is read back off the
// topic instead, where it is as durable as the order.
type CheckoutSaga interface {
	// CommitReservation turns the stock reserved for an order into a sale.
	//
	// It is driven by OrderPaid and not by OrderPlaced, because committing a
	// hold is selling the goods: inventory refuses to release a reservation it
	// has already committed, so a commit taken before the money is in leaves a
	// failed payment with nothing to give back.
	CommitReservation(ctx context.Context, event OrderPaidEvent) error

	// ReleaseReservation gives the held stock back. It is the compensating
	// step, driven by OrderCancelled however the order came to be cancelled.
	ReleaseReservation(ctx context.Context, event OrderCancelledEvent) error
}
