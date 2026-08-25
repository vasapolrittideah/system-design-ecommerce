package domain

import (
	"strings"

	"github.com/google/uuid"
)

// uuidCanonicalLen is the length of the 8-4-4-4-12 form.
const uuidCanonicalLen = 36

// OrderID identifies one purchase.
//
// A string rather than a uuid.UUID so that the form stored, the form on the
// proto, and the form compared here are one thing. The conversion that costs
// lives in the repository, which is already translating for pgx.
type OrderID string

// NewOrderID mints an identifier for an order that has never been stored.
//
// It is minted before the order is persisted, and before the stock is even
// reserved, because inventory takes the hold under this id — the reservation
// has to name the order it is for, and the row comes later.
func NewOrderID() OrderID { return OrderID(uuid.NewString()) }

// ParseOrderID validates an identifier that arrived from outside.
func ParseOrderID(s string) (OrderID, error) {
	parsed, err := parseUUID("id", s)

	return OrderID(parsed), err
}

// String returns the canonical form.
func (id OrderID) String() string { return string(id) }

// UserID is who placed the order.
//
// Deliberately opaque: nothing in this package knows what a user is, only that
// an order has exactly one and that a caller may read no others. It is taken
// from the verified identity on the call, never from a field.
type UserID string

// ParseUserID validates a user identifier that arrived from outside.
func ParseUserID(s string) (UserID, error) {
	parsed, err := parseUUID("user_id", s)

	return UserID(parsed), err
}

// String returns the canonical form.
func (id UserID) String() string { return string(id) }

// ReservationID is inventory's hold on the stock for this order.
//
// Opaque for the same reason UserID is: this package knows an order has a hold
// and that the hold can expire, not what a reservation is made of.
type ReservationID string

// ParseReservationID validates a reservation identifier that arrived from
// outside.
func ParseReservationID(s string) (ReservationID, error) {
	parsed, err := parseUUID("reservation_id", s)

	return ReservationID(parsed), err
}

// String returns the canonical form.
func (id ReservationID) String() string { return string(id) }

// parseUUID is the one place a UUID is checked, so that every identifier here
// refuses the same shapes and names the field the caller used.
func parseUUID(field, s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ValidationError{Field: field, Message: "is required"}
	}

	// uuid.Parse accepts urn: and brace-wrapped forms too, which would store a
	// second spelling of the same identifier and compare unequal to the first.
	if len(s) != uuidCanonicalLen {
		return "", ValidationError{Field: field, Message: "is not a canonical UUID"}
	}

	parsed, err := uuid.Parse(s)
	if err != nil {
		return "", ValidationError{Field: field, Message: "is not a UUID"}
	}

	return parsed.String(), nil
}
