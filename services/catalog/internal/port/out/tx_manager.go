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
	// Every write in this service reads the aggregate and writes it back, and
	// both halves belong inside fn: the version read outside a transaction is a
	// version that may already be stale by the time the update carrying it is
	// sent.
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}
