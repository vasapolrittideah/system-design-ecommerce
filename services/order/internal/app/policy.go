package app

import "time"

// Policy is what this service decides rather than the domain or a caller: how
// long an order may wait to be paid for, and where the current time comes from.
type Policy struct {
	// PaymentWindow is how long an order stays open for payment before the
	// saga gives up on it and compensates.
	//
	// It has to be shorter than inventory's INVENTORY_RESERVATION_TTL, and this
	// is the one coupling in the flow that fails silently in both directions.
	//
	// Too long, and the hold expires first: the customer pays, the order is
	// marked paid, and the commit that turns the hold into a sale is refused
	// because the reservation is gone — money taken for stock somebody else can
	// now buy. Too short is merely rude: a shopper who was still on a 3-D
	// Secure page finds their order cancelled.
	//
	// The margin has to cover everything between the last moment a payment can
	// start and the commit landing: the provider's answer, the outbox relay's
	// poll, and both consumers. Minutes, not seconds.
	PaymentWindow time.Duration

	// Now is the clock. A field rather than a call to time.Now, so that the
	// expiry rules can be driven from a test without one sleeping through a
	// window.
	Now func() time.Time
}
