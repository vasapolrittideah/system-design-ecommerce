// Package postgres is the driven adapter for storage: it maps between the
// domain aggregate and the rows sqlc generated, and turns pgx failures into
// classified errors.
//
// It is the only package in the service that knows an order is a row.
package postgres

import (
	"context"
	"math"

	"github.com/google/uuid"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/adapter/out/postgres/sqlc"
)

// topic is where this service's events belong. The service that owns an event
// decides its destination, so adding one never means editing a table somebody
// else also reads.
const topic = "ecommerce.order.events.v1"

// aggregateType names what these events happened to, in the outbox row and in
// nothing else — it is there for whoever reads the table.
const aggregateType = "order"

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

// queries binds sqlc to whichever handle is correct for this call: the
// transaction txmanager put on the context, or the pool when there is none. It
// is what lets the same method run inside a use case's tx.Do and outside one
// without either saying so.
func queriesFrom(ctx context.Context, pool txmanager.DBTX) *sqlc.Queries {
	return sqlc.New(txmanager.From(ctx, pool))
}

// narrow converts a count the domain keeps as an int to the int32 the columns
// are. The values that reach it — a line quantity and a page limit — are
// bounded far below this by the schema and the use case; the clamp is here so
// that the conversion cannot silently wrap if one day they are not.
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
