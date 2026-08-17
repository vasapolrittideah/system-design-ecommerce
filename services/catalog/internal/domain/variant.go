package domain

import (
	"maps"
	"strings"
	"time"
)

// The bounds a SKU has to satisfy, matching the pattern the proto declares.
const (
	skuMinLen = 3
	skuMaxLen = 64
)

// SKU is what the warehouse and every downstream service call one sellable
// thing. It is chosen by whoever creates the product rather than generated here,
// because it usually already exists on a shelf somewhere.
//
// Uppercased, which is what the schema relies on: SKUs are stored already
// uppercased, so uniqueness is a plain UNIQUE constraint, and
// CHECK (sku = upper(sku)) fails loudly on a writer that skipped this type
// instead of quietly creating a second SKU indistinguishable to a human.
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

// The bounds on the attribute map, matching what the proto declares.
const (
	attributesMaxPairs   = 20
	attributeKeyMaxLen   = 32
	attributeValueMaxLen = 128
)

// newAttributes copies and checks what distinguishes a variant from its
// siblings.
//
// No key means anything here — that is the whole point of the map, and it is why
// this catalog can sell shirts and rice without knowing which it is doing. What
// is checked is shape: keys that survive being a JSON object key, a URL query
// parameter, and a column heading, and a size that keeps one variant's
// description from becoming a document.
//
// An empty map comes back nil, so a variant with no attributes round-trips
// through a snapshot as one value rather than alternating between nil and an
// empty non-nil map.
func newAttributes(attributes map[string]string) (map[string]string, error) {
	if len(attributes) == 0 {
		return nil, nil
	}

	if len(attributes) > attributesMaxPairs {
		return nil, ValidationError{Field: "attributes", Message: "has more than 20 entries"}
	}

	for key, value := range attributes {
		if !validAttributeKey(key) {
			return nil, ValidationError{
				Field:   "attributes",
				Message: "has a key that is not 1 to 32 characters of a-z, 0-9, and underscores starting with a letter",
			}
		}

		if value == "" || len(value) > attributeValueMaxLen {
			return nil, ValidationError{
				Field:   "attributes",
				Message: "has a value that is empty or longer than 128 characters",
			}
		}
	}

	return maps.Clone(attributes), nil
}

// validAttributeKey reports whether a key is lowercase snake case.
func validAttributeKey(key string) bool {
	if key == "" || len(key) > attributeKeyMaxLen {
		return false
	}

	if key[0] < 'a' || key[0] > 'z' {
		return false
	}

	for i := range len(key) {
		c := key[i]

		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}

	return true
}

// Variant is the unit that is actually sold: one SKU, one price, and whatever
// distinguishes it from its siblings.
//
// It is part of the Product aggregate and has no constructor of its own —
// Product.AddVariant is the only way to make one, because whether a new variant
// is allowed at all depends on the product's state and on the prices already on
// it.
type Variant struct {
	id         VariantID
	productID  ProductID
	sku        SKU
	price      Money
	attributes map[string]string

	// Set by the database on insert and read back, never chosen here. CreatedAt
	// being the zero time is also how a repository tells a variant that has
	// never been written from one it is updating.
	createdAt time.Time
	updatedAt time.Time

	// version is the optimistic lock: the value an UPDATE must carry and bump,
	// so a concurrent writer affects zero rows and finds out.
	version int
}

// ID returns the identifier a cart line and an order line refer to.
func (v *Variant) ID() VariantID { return v.id }

// ProductID returns the aggregate this variant belongs to.
func (v *Variant) ProductID() ProductID { return v.productID }

// SKU returns the uppercased stock-keeping unit.
func (v *Variant) SKU() SKU { return v.sku }

// Price returns what this variant costs.
func (v *Variant) Price() Money { return v.price }

// Attributes returns a copy, so a caller ranging over them cannot rewrite the
// aggregate's idea of what this variant is.
func (v *Variant) Attributes() map[string]string { return cloneAttributes(v.attributes) }

// CreatedAt is the zero time until the row has been written.
func (v *Variant) CreatedAt() time.Time { return v.createdAt }

// UpdatedAt is the zero time until the row has been written.
func (v *Variant) UpdatedAt() time.Time { return v.updatedAt }

// Version is the value an UPDATE must carry to win the optimistic lock.
func (v *Variant) Version() int { return v.version }

// VariantSnapshot is the whole state of a variant as it is stored.
type VariantSnapshot struct {
	ID         VariantID
	ProductID  ProductID
	SKU        SKU
	Price      Money
	Attributes map[string]string

	// CreatedAt is the zero time for a variant that has never been persisted,
	// which is what a repository writing a product reads to decide between an
	// INSERT and an UPDATE.
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int
}

// ReconstituteVariant rebuilds a variant from storage.
//
// It validates nothing, and that is deliberate: a rule tightened afterwards must
// not make existing products unreadable. New values come in through
// Product.AddVariant and Product.UpdateVariant, which do validate.
func ReconstituteVariant(s VariantSnapshot) *Variant {
	return &Variant{
		id:         s.ID,
		productID:  s.ProductID,
		sku:        s.SKU,
		price:      s.Price,
		attributes: s.Attributes,
		createdAt:  s.CreatedAt,
		updatedAt:  s.UpdatedAt,
		version:    s.Version,
	}
}

// Snapshot returns the variant's state for a repository to persist.
func (v *Variant) Snapshot() VariantSnapshot {
	return VariantSnapshot{
		ID:         v.id,
		ProductID:  v.productID,
		SKU:        v.sku,
		Price:      v.price,
		Attributes: cloneAttributes(v.attributes),
		CreatedAt:  v.createdAt,
		UpdatedAt:  v.updatedAt,
		Version:    v.version,
	}
}

// cloneAttributes is maps.Clone with a nil result for an empty input.
func cloneAttributes(attributes map[string]string) map[string]string {
	if len(attributes) == 0 {
		return nil
	}

	return maps.Clone(attributes)
}
