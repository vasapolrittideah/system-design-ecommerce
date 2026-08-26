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

// PaymentSucceededEvent is what arrived on the payment service's topic to say
// the money for an order is in.
//
// It carries no amount. What was collected is a fact about the payment, and an
// order that checked it here would be re-deciding a question the payment
// service already answered — with a number this service would have to be told
// how to compare.
type PaymentSucceededEvent struct {
	EventID string

	OrderID   string
	PaymentID string
}

// CheckoutSaga is what this service's own consumers drive: the half of
// checkout that runs after an outcome is durably persisted rather than inside
// the transaction that persisted it.
//
// Two topics reach it. The payment service's says the money is in and moves the
// order's own state machine; this service's own says what became of the order
// and makes the one call to inventory that follows. They are deliberately two
// hops rather than one — a payment is a fact about a payment, an order being
// paid is a fact about an order, and collapsing them would put the order's
// state machine inside a consumer that does not own it.
//
// There is no method for a payment that failed, and that absence is the design:
// a declined card leaves the order open for the customer to try another, so
// what ends an unpaid order is running out of time rather than one attempt
// going wrong. The transition that ends it is the aggregate's Cancel, and its
// caller is the timeout worker.
type CheckoutSaga interface {
	// MarkPaid records against the order that the money is in.
	//
	// It does not touch inventory. Marking the order raises OrderPaid, and the
	// commit that turns the hold into a sale is driven by that — which is what
	// keeps the call as durable as the transition, rather than being made
	// straight after a commit by a process that may not survive to make it.
	MarkPaid(ctx context.Context, event PaymentSucceededEvent) error

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
