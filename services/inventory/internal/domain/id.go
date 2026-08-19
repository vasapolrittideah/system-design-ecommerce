package domain

import (
	"strings"

	"github.com/google/uuid"
)

// uuidCanonicalLen is the length of the 8-4-4-4-12 form.
const uuidCanonicalLen = 36

// ReservationID identifies one order's hold on stock.
//
// A string rather than a uuid.UUID so that the form stored, the form on the
// proto, and the form compared here are one thing. The conversion that costs
// lives in the repository, which is already translating for pgx.
type ReservationID string

// NewReservationID mints an identifier for a reservation that has never been
// stored. It panics only if the operating system cannot supply randomness, which
// is the right behaviour: the alternative is a predictable identifier.
func NewReservationID() ReservationID { return ReservationID(uuid.NewString()) }

// ParseReservationID validates an identifier that arrived from outside.
func ParseReservationID(s string) (ReservationID, error) {
	parsed, err := parseUUID("id", s)

	return ReservationID(parsed), err
}

// String returns the canonical form.
func (id ReservationID) String() string { return string(id) }

// OrderID is the order a reservation was taken for.
//
// It is the one identifier here that this service does not mint, and it is
// deliberately opaque: nothing in this package knows what an order is, only that
// two reservations may not share one. That is what keeps the dependency a
// correlation key rather than a second service's concepts leaking in.
type OrderID string

// ParseOrderID validates an order identifier that arrived from outside.
func ParseOrderID(s string) (OrderID, error) {
	parsed, err := parseUUID("order_id", s)

	return OrderID(parsed), err
}

// String returns the canonical form.
func (id OrderID) String() string { return string(id) }

// StockItemID identifies the row holding one SKU's counts. It exists because
// every table here carries a uuid primary key; the key anything actually looks a
// count up by is the SKU.
type StockItemID string

// NewStockItemID mints an identifier for a stock item that has never been
// stored.
func NewStockItemID() StockItemID { return StockItemID(uuid.NewString()) }

// String returns the canonical form.
func (id StockItemID) String() string { return string(id) }

// LineID identifies one line of a reservation.
type LineID string

// NewLineID mints an identifier for a reservation line.
func NewLineID() LineID { return LineID(uuid.NewString()) }

// String returns the canonical form.
func (id LineID) String() string { return string(id) }

// parseUUID checks an identifier and returns it in the one form this service
// compares.
//
// The proto already constrains these fields with protovalidate, so a malformed
// id rarely reaches here over gRPC. It is checked anyway because an id that
// reaches the repository unchecked becomes a pgx parse failure — reported as
// Internal, which says the service is broken when the caller made a typo.
func parseUUID(field, s string) (string, error) {
	invalid := ValidationError{Field: field, Message: "not a UUID"}

	// The length check is not redundant. uuid.Validate also accepts the braced,
	// urn:uuid:, and unhyphenated spellings, which would make one row reachable
	// under four different keys.
	if len(s) != uuidCanonicalLen {
		return "", invalid
	}

	// Lowercased because that is how PostgreSQL renders a uuid column on the way
	// back out, and an identifier that compares unequal to itself depending on
	// which side of the database it came from is a bug that surfaces far from
	// its cause.
	s = strings.ToLower(s)

	if uuid.Validate(s) != nil {
		return "", invalid
	}

	return s, nil
}
