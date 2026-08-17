package domain

import (
	"strings"

	"github.com/google/uuid"
)

// uuidCanonicalLen is the length of the 8-4-4-4-12 form.
const uuidCanonicalLen = 36

// ProductID is a UUID in its canonical lowercase form.
//
// A string rather than a uuid.UUID so that the form stored, the form on the
// proto, and the form compared here are one thing. The conversion that costs
// lives in the repository, which is already translating for pgx.
type ProductID string

// NewProductID mints an identifier for a product that has never been stored. It
// panics only if the operating system cannot supply randomness, which is the
// right behaviour: the alternative is a predictable identifier.
func NewProductID() ProductID { return ProductID(uuid.NewString()) }

// ParseProductID validates an identifier that arrived from outside.
func ParseProductID(s string) (ProductID, error) {
	parsed, err := parseUUID("product_id", s)

	return ProductID(parsed), err
}

// String returns the canonical form.
func (id ProductID) String() string { return string(id) }

// VariantID identifies one sellable unit. It is what a cart line and an order
// line refer to, so it outlives every description around it.
type VariantID string

// NewVariantID mints an identifier for a variant that has never been stored.
func NewVariantID() VariantID { return VariantID(uuid.NewString()) }

// ParseVariantID validates an identifier that arrived from outside.
func ParseVariantID(s string) (VariantID, error) {
	parsed, err := parseUUID("variant_id", s)

	return VariantID(parsed), err
}

// String returns the canonical form.
func (id VariantID) String() string { return string(id) }

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
