// Package postgres is the driven adapter for storage: it maps between the domain
// aggregates and the rows sqlc generated, and turns pgx failures into classified
// errors.
//
// It is where the rule the domain deliberately does not hold actually lives. The
// movement statements carry their own predicate — `available >= quantity` — so
// the decision about whether stock can be taken is made by the server against
// the current row rather than by this process against a number it read earlier.
// A zero row count is that decision arriving, and turning it back into the
// warehouse's vocabulary is most of what this package does.
package postgres

import (
	"errors"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
)

// uniqueViolation is PostgreSQL's SQLSTATE for a duplicate key.
const uniqueViolation = "23505"

// The constraints this package answers for by name. Matching on the name rather
// than on the code alone is what keeps a primary key collision — two random
// UUIDs coming out the same, i.e. a bug — reported as Internal instead of as
// something the caller did.
const (
	skuConstraint       = "stock_items_sku_key"
	heldOrderConstraint = "reservations_held_order_idx"
)

// isConstraintViolation reports whether err is a unique violation of the named
// constraint.
func isConstraintViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == uniqueViolation && pgErr.ConstraintName == constraint
}

// parseUUID converts an identifier for pgx.
//
// Unreachable through the domain constructors and the parse the use case runs
// first, which is exactly why the failure is Internal: reaching it means an
// aggregate was built with an id this package cannot store.
func parseUUID(what, id string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.UUID{}, errorx.Wrap(err, errorx.KindInternal, "parse %s", what)
	}

	return parsed, nil
}

// narrow converts a count the domain keeps as an int to the int32 the columns
// are. The values that reach it — a version and a sweep limit — are bounded far
// below this by the schema and the use case; the clamp is here so that the
// conversion cannot silently wrap if one day they are not.
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
