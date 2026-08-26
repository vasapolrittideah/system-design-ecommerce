package domain

import (
	"strings"

	"github.com/google/uuid"
)

// uuidCanonicalLen is the length of the 8-4-4-4-12 form.
const uuidCanonicalLen = 36

// providerReferenceMax bounds what a provider may call its own charge. Generous
// because the value is theirs to choose, and bounded because it is stored.
const providerReferenceMax = 255

// PaymentID identifies one attempt to collect.
//
// A string rather than a uuid.UUID so that the form stored, the form on the
// proto, and the form compared here are one thing. The conversion that costs
// lives in the repository, which is already translating for pgx.
type PaymentID string

// NewPaymentID mints an identifier for an attempt that has never been stored.
//
// It is minted before the row exists because it is also what the provider is
// given as its idempotency key: a retry after an ambiguous timeout has to reach
// the same charge, and it can only do that if the key predates the first call.
func NewPaymentID() PaymentID { return PaymentID(uuid.NewString()) }

// ParsePaymentID validates an identifier that arrived from outside.
func ParsePaymentID(s string) (PaymentID, error) {
	parsed, err := parseUUID("id", s)

	return PaymentID(parsed), err
}

// String returns the canonical form.
func (id PaymentID) String() string { return string(id) }

// OrderID is the order being collected for.
//
// Deliberately opaque: nothing in this package knows what an order is made of,
// only that an attempt is made against exactly one and that its amount was
// copied from it.
type OrderID string

// ParseOrderID validates an order identifier that arrived from outside.
func ParseOrderID(s string) (OrderID, error) {
	parsed, err := parseUUID("order_id", s)

	return OrderID(parsed), err
}

// String returns the canonical form.
func (id OrderID) String() string { return string(id) }

// UserID is who is paying.
//
// Opaque for the reason OrderID is. It is taken from the verified identity on
// the call, never from a field.
type UserID string

// ParseUserID validates a user identifier that arrived from outside.
func ParseUserID(s string) (UserID, error) {
	parsed, err := parseUUID("user_id", s)

	return UserID(parsed), err
}

// String returns the canonical form.
func (id UserID) String() string { return string(id) }

// ProviderReference is what the provider calls this charge.
//
// Not a UUID and not checked as one: its shape belongs to whoever is collecting
// the money, and a system that insisted on a format here would refuse to record
// a charge it had already made. Only its presence and its length are this
// service's business.
type ProviderReference string

// ParseProviderReference checks a reference the provider gave us.
func ParseProviderReference(s string) (ProviderReference, error) {
	s = strings.TrimSpace(s)

	switch {
	case s == "":
		return "", ValidationError{Field: "provider_reference", Message: "is required"}
	case len(s) > providerReferenceMax:
		return "", ValidationError{Field: "provider_reference", Message: "is too long"}
	}

	return ProviderReference(s), nil
}

// String returns the reference as the provider spelled it.
func (r ProviderReference) String() string { return string(r) }

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
