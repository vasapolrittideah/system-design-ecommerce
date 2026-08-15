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
	// Work that must survive fn failing cannot go inside it. Revoking a token
	// family on detected reuse is exactly that case: it is a durable
	// consequence of the error being returned, so it happens in its own write.
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}
