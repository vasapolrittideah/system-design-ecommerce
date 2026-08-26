// Package postgres is the driven adapter for storage: it maps between the
// domain aggregate and the rows sqlc generated, and turns pgx failures into
// classified errors.
//
// It is the only package in the service that knows an attempt is a row.
package postgres

import (
	"context"
	"errors"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/adapter/out/postgres/sqlc"
)

// topic is where this service's events belong. The service that owns an event
// decides its destination, so adding one never means editing a table somebody
// else also reads.
const topic = "ecommerce.payment.events.v1"

// aggregateType names what these events happened to, in the outbox row and in
// nothing else — it is there for whoever reads the table.
const aggregateType = "payment"

// uniqueViolation is PostgreSQL's SQLSTATE for a duplicate key.
const uniqueViolation = "23505"

// unresolvedConstraint is the partial unique index that allows one unresolved
// attempt per order. Matched by name rather than by code alone, so that a
// primary key collision — two random UUIDs coming out the same, i.e. a bug —
// stays Internal instead of being reported as something the caller did.
const unresolvedConstraint = "payments_one_unresolved_per_order_idx"

// isConstraintViolation reports whether err is a unique violation of the named
// constraint.
func isConstraintViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == uniqueViolation && pgErr.ConstraintName == constraint
}

// parseID converts a domain identifier for pgx.
//
// A failure here is unreachable through the domain constructors, which is
// exactly why it is Internal: reaching it means an aggregate was built with an
// id this package cannot store.
func parseID(id string, what string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.UUID{}, errorx.Wrap(err, errorx.KindInternal, "parse %s", what)
	}

	return parsed, nil
}

// queriesFrom binds sqlc to whichever handle is correct for this call: the
// transaction txmanager put on the context, or the pool when there is none. It
// is what lets the same method run inside a use case's tx.Do and outside one
// without either saying so.
func queriesFrom(ctx context.Context, pool txmanager.DBTX) *sqlc.Queries {
	return sqlc.New(txmanager.From(ctx, pool))
}

// narrow converts a value the domain keeps as an int to the int32 the column
// is. The only one that reaches it is the optimistic-locking version, which the
// database itself increments and which the schema bounds far below this; the
// clamp is here so that the conversion cannot silently wrap if that changes.
func narrow(value int) int32 {
	switch {
	case value < 0:
		return 0
	case value > math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(value)
	}
}
