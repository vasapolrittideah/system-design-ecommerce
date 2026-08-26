package out

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
)

// PayableOrder is as much of an order as starting an attempt needs.
//
// It carries no status. Whether an order may still be paid for is the order
// service's rule, and the adapter that speaks to it answers the question rather
// than handing this package an enum to interpret — a copy of another service's
// state machine here would be one that stops agreeing with the original the
// first time a state is added.
type PayableOrder struct {
	ID domain.OrderID

	// Total is what the order came to. It is copied onto the attempt, so that
	// what was sent to the provider stays readable next to what the provider
	// says it charged, even after the order has moved on.
	Total domain.Money

	// Payable is the order service's answer to whether this order is still
	// waiting to be paid for. False for one already paid or cancelled.
	Payable bool
}

// OrderGateway is what this service may ask of the order service.
//
// Reading is synchronous and happens before an attempt is written, because the
// caller needs the answer now: how much to charge is not a number a client may
// send, and an order that is already paid or cancelled must not be charged for
// at all.
type OrderGateway interface {
	// GetOrder reads one order.
	//
	// The caller's identity travels with the call, so an order belonging to
	// somebody else comes back as not found rather than as somebody else's
	// total — which is why this method takes no user and why this service does
	// not check ownership a second time.
	GetOrder(ctx context.Context, orderID domain.OrderID) (PayableOrder, error)
}
