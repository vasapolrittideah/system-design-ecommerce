// Package txmanager gives use cases a transaction boundary they can open
// without knowing that PostgreSQL is underneath.
//
// A use case wraps the work that has to be atomic:
//
//	err := tx.Do(ctx, func(ctx context.Context) error {
//		order.MarkPaid(paidAt)
//		return orders.Save(ctx, order)   // aggregate + outbox rows
//	})
//
// and a repository picks the transaction back off the context, falling back to
// the pool when it is called outside one:
//
//	q := sqlcgen.New(txmanager.From(ctx, r.pool))
//
// The transaction travels in the context rather than through the call chain
// because it has to reach several repositories that know nothing about each
// other: an aggregate and the outbox rows announcing what happened to it must
// commit together or the rest of the system never hears about the change.
package txmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Manager opens transactions against a single pool.
type Manager struct {
	pool *pgxpool.Pool
}

// New builds a Manager over pool.
func New(pool *pgxpool.Pool) *Manager {
	return &Manager{pool: pool}
}

// Do runs fn inside a transaction, committing when it returns nil and rolling
// back otherwise. The context handed to fn carries the transaction, so every
// repository fn touches writes through it.
//
// A nested Do joins the transaction already in flight instead of opening a
// second one. Savepoints would let an inner rollback succeed while the outer
// commit goes through anyway, leaving half of a use case persisted; one use
// case is one transaction.
func (m *Manager) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFrom(ctx); ok {
		return fn(ctx)
	}

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("txmanager: begin: %w", err)
	}

	// A panic must not leave the transaction holding its locks until the
	// connection is reaped, so unwind it here and let the panic continue.
	defer func() {
		if p := recover(); p != nil {
			_ = rollback(ctx, tx)
			panic(p)
		}
	}()

	if err := fn(withTx(ctx, tx)); err != nil {
		// The caller's error is the one worth acting on; a rollback failure is
		// joined onto it rather than replacing it.
		return errors.Join(err, rollback(ctx, tx))
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("txmanager: commit: %w", err)
	}

	return nil
}

// rollback undoes tx.
//
// It deliberately ignores ctx's cancellation: the usual reason a transaction is
// being rolled back is that the caller gave up or timed out, and rolling back
// on a dead context makes pgx destroy the connection instead of returning a
// clean one to the pool. A transaction already resolved by the server — after a
// failed commit, say — reports ErrTxClosed, which is the state we wanted.
func rollback(ctx context.Context, tx pgx.Tx) error {
	if err := tx.Rollback(context.WithoutCancel(ctx)); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return fmt.Errorf("txmanager: rollback: %w", err)
	}

	return nil
}
