package out

import "context"

// TxManager runs work inside one transaction.
//
// A port rather than *txmanager.Manager directly, so a use case can be tested
// against a mock instead of a pool. It is satisfied as-is by pkg/txmanager,
// which is why there is no adapter for it.
type TxManager interface {
	// Do commits when fn returns nil and rolls back otherwise, handing fn a
	// context that carries the transaction — which is how the repositories fn
	// calls end up writing through it without any of them saying so.
	//
	// Reserving is the reason every write here needs one: a hold is several
	// stock rows and a reservation, and an order that decremented three SKUs
	// and failed on the fourth must leave the shelves as it found them.
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}
