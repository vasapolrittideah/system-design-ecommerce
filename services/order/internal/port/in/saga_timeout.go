package in

import "context"

// SagaTimeout forces the checkout saga to compensate an order nobody paid for.
//
// A driving port of its own rather than one more method on OrderUseCase,
// because its caller is a timer in a separate process: nothing reachable over
// gRPC may cancel other people's orders in bulk, and a handler that could would
// be one accidental route away from a client doing it.
//
// It is what makes an unpaid order end at all. A declined card leaves the order
// open so the customer can try another, so nothing in the event flow ever
// concludes that an order is over — running out of time is the only thing that
// does, and this is where that is decided.
type SagaTimeout interface {
	// ExpireStaleOrders cancels at most limit orders whose payment window has
	// run out, and reports how many it moved. Returning fewer than limit is how
	// the caller learns it has caught up.
	ExpireStaleOrders(ctx context.Context, limit int) (int, error)
}
