package in

import "context"

// OrderPlacedEvent is what arrived on this service's own OrderPlaced topic,
// as much of it as continuing the saga needs.
type OrderPlacedEvent struct {
	// EventID is the envelope's id, claimed in the inbox so a redelivery does
	// not call inventory a second time.
	EventID string

	OrderID       string
	ReservationID string
}

// CheckoutSaga is what this service's own consumer of OrderPlaced drives: the
// half of checkout that runs after the order is durably persisted rather than
// inside the request that persisted it.
type CheckoutSaga interface {
	// CommitReservation turns the stock reserved for an order into a sale.
	//
	// The call to inventory that does this cannot be made from inside the
	// transaction that persists the order — a call made right after commit
	// is a write nothing would retry if the process died between the two —
	// so the trigger is read back off the topic instead, where it is as
	// durable as the order.
	CommitReservation(ctx context.Context, event OrderPlacedEvent) error
}
