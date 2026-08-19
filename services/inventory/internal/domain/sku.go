package domain

import "strings"

// The bounds a SKU has to satisfy, matching the pattern the proto declares.
const (
	skuMinLen = 3
	skuMaxLen = 64
)

// SKU is what the warehouse and every downstream service call one sellable
// thing. It is chosen wherever the product was created rather than generated
// here, because it usually already exists on a shelf somewhere.
//
// Uppercased, which is what the schema relies on: SKUs are stored already
// uppercased, so uniqueness is a plain UNIQUE constraint, and
// CHECK (sku = upper(sku)) fails loudly on a writer that skipped this type
// instead of quietly creating a second SKU indistinguishable to a human.
//
// The same type exists in the catalog service, and that is the trade rule 5 of
// CLAUDE.md describes rather than an oversight. Two copies can diverge; a shared
// one would make every SKU rule in this repo a cross-service change.
type SKU string

// NewSKU normalises and checks a stock-keeping unit.
//
// Letters, digits, and hyphens, not starting with a hyphen. The character set is
// narrow on purpose: this string is printed on labels, typed into search boxes,
// and pasted into spreadsheets, and every one of those goes wrong with spaces or
// case in it.
func NewSKU(s string) (SKU, error) {
	invalid := ValidationError{Field: "sku", Message: "must be 3 to 64 characters of A-Z, 0-9, and hyphens"}

	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) < skuMinLen || len(s) > skuMaxLen {
		return "", invalid
	}

	for i := range len(s) {
		switch c := s[i]; {
		case (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
		case c == '-' && i > 0:
		default:
			return "", invalid
		}
	}

	return SKU(s), nil
}

// String returns the uppercased unit.
func (s SKU) String() string { return string(s) }
