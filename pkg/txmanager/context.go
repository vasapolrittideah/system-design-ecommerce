package txmanager

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX is what a repository needs to run a query, and what sqlc's generated
// constructors accept. Both *pgxpool.Pool and pgx.Tx satisfy it, which is what
// lets the same repository code run inside a transaction or on its own.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ctxKey is unexported so nothing outside this package can put a transaction
// into a context or take one out. Opening and closing a transaction is Do's
// job, and code that could reach the raw pgx.Tx would be able to commit half a
// use case.
type ctxKey struct{}

func withTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, ctxKey{}, tx)
}

func txFrom(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(ctxKey{}).(pgx.Tx)

	return tx, ok
}

// From returns the transaction ctx is running inside, or fallback when there is
// none.
//
// Repositories call it on every query instead of choosing between a pool and a
// transaction themselves:
//
//	q := sqlcgen.New(txmanager.From(ctx, r.pool))
//
// A read that runs outside any use case still works, and the same method called
// from inside Do joins that transaction without a second code path.
func From(ctx context.Context, fallback DBTX) DBTX {
	if tx, ok := txFrom(ctx); ok {
		return tx
	}

	return fallback
}
