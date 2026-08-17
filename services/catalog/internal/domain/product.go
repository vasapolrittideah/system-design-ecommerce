// Package domain holds the catalog service's aggregates and the rules that
// protect them: what a product is, what makes a SKU or a price valid, which
// states a product may move between, and which of those rules the database is
// also holding.
//
// It knows nothing about what is being sold. A rule here that read an attribute
// key would be the moment this catalog stopped being able to sell both shirts
// and rice.
package domain

import (
	"slices"
	"strings"
	"time"
)

// The bounds on a product's text, matching what the proto declares.
const (
	nameMaxLen        = 200
	descriptionMaxLen = 4000
	categoryMaxLen    = 64
)

// Category is a slug naming where a product sits in the storefront, or the empty
// string for uncategorised.
//
// Deliberately not an aggregate: nothing here needs a tree, localised names, or
// a category that can be renamed in one place. Stored already lowercased, which
// is what lets a filter be a plain equality rather than a functional index.
type Category string

// NewCategory normalises and checks a slug.
//
// Lowercase alphanumeric segments joined by a single hyphen or slash —
// "clothing", "clothing/shirts". The empty string is a valid category and means
// the product has not been placed anywhere yet, which a draft is allowed to be.
func NewCategory(s string) (Category, error) {
	invalid := ValidationError{
		Field:   "category",
		Message: "must be lowercase alphanumeric segments joined by a hyphen or a slash",
	}

	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", nil
	}

	if len(s) > categoryMaxLen {
		return "", ValidationError{Field: "category", Message: "is longer than 64 characters"}
	}

	for i := range len(s) {
		switch c := s[i]; {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
		case c == '-' || c == '/':
			// A separator joins two segments, so it can neither open nor close
			// the slug nor follow another separator.
			if i == 0 || i == len(s)-1 || s[i-1] == '-' || s[i-1] == '/' {
				return "", invalid
			}
		default:
			return "", invalid
		}
	}

	return Category(s), nil
}

// String returns the normalised slug.
func (c Category) String() string { return string(c) }

// ProductStatus is where a product sits in its lifecycle. The values are the
// strings the status column stores, so the CHECK constraint in the migration and
// this type cannot drift apart in a way that only shows up on a write.
type ProductStatus string

const (
	// StatusDraft is being written: never shown to a shopper, never buyable, and
	// the only state from which a product may still be discarded outright.
	StatusDraft ProductStatus = "draft"

	// StatusActive is published and buyable.
	StatusActive ProductStatus = "active"

	// StatusArchived is withdrawn from sale, permanently. Nothing is deleted:
	// orders placed last year still name these variants.
	StatusArchived ProductStatus = "archived"
)

// String returns the stored form.
func (s ProductStatus) String() string { return string(s) }

// Product is the aggregate: a thing the storefront describes, and the variants
// of it that can be bought.
//
// Its fields are unexported because several of them are invariants rather than
// data. A product is never in a state this package cannot name, its variants
// never disagree about currency, and no two of them share a SKU — none of which
// survives callers assigning to fields.
//
// The aggregate is loaded and saved whole, which is what makes those last two
// checkable at all: a variant written on its own could not see the prices it has
// to agree with.
type Product struct {
	id          ProductID
	name        string
	description string
	category    Category
	status      ProductStatus
	variants    []*Variant

	// Set by the database on insert and read back, never chosen here: two
	// replicas disagree about the current time by more than the ordering of two
	// products is worth.
	createdAt time.Time
	updatedAt time.Time

	// version is the optimistic lock: the value an UPDATE must carry and bump,
	// so a concurrent writer affects zero rows and finds out.
	version int
}

// NewProduct builds a draft that has never been persisted.
//
// It starts with no variants, because a product is usually named and described
// before anyone has decided what it costs. Publish is where that stops being
// allowed.
//
// The identifier is minted here rather than by the database, because the domain
// events an aggregate raises carry that id and are written to the outbox in the
// same transaction as the row.
func NewProduct(name, description string, category Category) (*Product, error) {
	name, err := validateName(name)
	if err != nil {
		return nil, err
	}

	description, err = validateDescription(description)
	if err != nil {
		return nil, err
	}

	return &Product{
		id:          NewProductID(),
		name:        name,
		description: description,
		category:    category,
		status:      StatusDraft,
		version:     1,
	}, nil
}

// UpdateDetails replaces everything about a product that is description rather
// than commerce.
//
// Wholesale rather than field by field: with three fields a partial update buys
// nothing but a second way to be wrong about which of them the caller meant to
// clear.
func (p *Product) UpdateDetails(name, description string, category Category) error {
	if err := p.ensureWritable(); err != nil {
		return err
	}

	name, err := validateName(name)
	if err != nil {
		return err
	}

	description, err = validateDescription(description)
	if err != nil {
		return err
	}

	p.name = name
	p.description = description
	p.category = category

	return nil
}

// AddVariant adds a sellable unit.
//
// Allowed on a live product as well as a draft — a new size arriving is not a
// reason to take the product off the storefront.
func (p *Product) AddVariant(sku SKU, price Money, attributes map[string]string) error {
	if err := p.ensureWritable(); err != nil {
		return err
	}

	if sku == "" {
		return ValidationError{Field: "sku", Message: "is required"}
	}

	if (price == Money{}) {
		return ValidationError{Field: "price", Message: "is required"}
	}

	attributes, err := newAttributes(attributes)
	if err != nil {
		return err
	}

	if slices.ContainsFunc(p.variants, func(v *Variant) bool { return v.sku == sku }) {
		return ErrDuplicateSKU
	}

	if err := p.ensureCurrency(price, ""); err != nil {
		return err
	}

	p.variants = append(p.variants, &Variant{
		id:         NewVariantID(),
		productID:  p.id,
		sku:        sku,
		price:      price,
		attributes: attributes,
		version:    1,
	})

	return nil
}

// UpdateVariant reprices a variant and restates what distinguishes it.
//
// The SKU is not among the things it can change: orders, carts, and the
// warehouse all refer to a variant by that string, so renaming one would rename
// something other systems have already written down.
func (p *Product) UpdateVariant(id VariantID, price Money, attributes map[string]string) error {
	if err := p.ensureWritable(); err != nil {
		return err
	}

	if (price == Money{}) {
		return ValidationError{Field: "price", Message: "is required"}
	}

	attributes, err := newAttributes(attributes)
	if err != nil {
		return err
	}

	index := slices.IndexFunc(p.variants, func(v *Variant) bool { return v.id == id })
	if index < 0 {
		return ErrVariantNotFound
	}

	// The variant being repriced is excluded from the currency check, so a
	// product with one variant can be moved to another currency outright while
	// one with several cannot be split across two.
	if err := p.ensureCurrency(price, id); err != nil {
		return err
	}

	p.variants[index].price = price
	p.variants[index].attributes = attributes

	return nil
}

// Publish moves a product onto the storefront.
//
// Publishing one that is already active succeeds and changes nothing: it is a
// state the caller wants to reach rather than a change they are making, and an
// admin clicking twice should not be told they made a mistake.
func (p *Product) Publish() error {
	if err := p.ensureWritable(); err != nil {
		return err
	}

	if len(p.variants) == 0 {
		return ErrProductHasNoVariants
	}

	p.status = StatusActive

	return nil
}

// Archive withdraws a product from sale, and is idempotent for the same reason
// Publish is.
//
// One-way on purpose. Reviving an archived product silently revives whatever was
// wrong with it, and the price a shopper is quoted then comes from a decision
// nobody made recently; a product that should sell again is a new one.
func (p *Product) Archive() {
	p.status = StatusArchived
}

// ID returns the product's identifier.
func (p *Product) ID() ProductID { return p.id }

// Name returns what the storefront calls this product.
func (p *Product) Name() string { return p.name }

// Description returns the storefront copy, which may be empty.
func (p *Product) Description() string { return p.description }

// Category returns the slug, which is empty for an uncategorised product.
func (p *Product) Category() Category { return p.category }

// Status returns the lifecycle state.
func (p *Product) Status() ProductStatus { return p.status }

// Variants returns a copy of the slice, so a caller cannot add or remove one
// behind the aggregate's back. The variants themselves are immutable from
// outside this package.
func (p *Product) Variants() []*Variant { return slices.Clone(p.variants) }

// CreatedAt is the zero time until the row has been written.
func (p *Product) CreatedAt() time.Time { return p.createdAt }

// UpdatedAt is the zero time until the row has been written.
func (p *Product) UpdatedAt() time.Time { return p.updatedAt }

// Version is the value an UPDATE must carry to win the optimistic lock.
func (p *Product) Version() int { return p.version }

// ensureWritable refuses the writes an archived product may not take. Every
// state transition and every edit goes through it, so archiving is the one check
// that cannot be forgotten by adding a method.
func (p *Product) ensureWritable() error {
	if p.status == StatusArchived {
		return ErrProductArchived
	}

	return nil
}

// ensureCurrency reports whether price agrees with what the product's other
// variants are sold in, ignoring the variant identified by except.
//
// The rule exists because the sum happens elsewhere: a cart adding two
// currencies produces a number that is not a price in either of them, and by
// then this aggregate is nowhere in the call stack.
func (p *Product) ensureCurrency(price Money, except VariantID) error {
	for _, variant := range p.variants {
		if variant.id == except {
			continue
		}

		if variant.price.Currency() != price.Currency() {
			return ErrCurrencyMismatch
		}
	}

	return nil
}

// validateName checks the one piece of text a product cannot go without.
func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)

	switch {
	case name == "":
		return "", ValidationError{Field: "name", Message: "is required"}
	case len(name) > nameMaxLen:
		return "", ValidationError{Field: "name", Message: "is longer than 200 characters"}
	}

	return name, nil
}

// validateDescription bounds the storefront copy. Empty is allowed: a draft is
// usually named before it is described.
func validateDescription(description string) (string, error) {
	description = strings.TrimSpace(description)
	if len(description) > descriptionMaxLen {
		return "", ValidationError{Field: "description", Message: "is longer than 4000 characters"}
	}

	return description, nil
}

// ProductSnapshot is the whole state of a product as it is stored, so a
// repository can write the rows and rebuild the aggregate without its fields
// being exported to everything else that imports this package.
//
// It is the persistence shape, not the API shape: what this service tells other
// services about a product is ecommerce.catalog.v1.Product, which carries no
// version.
type ProductSnapshot struct {
	ID          ProductID
	Name        string
	Description string
	Category    Category
	Status      ProductStatus
	Variants    []VariantSnapshot
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Version     int
}

// ReconstituteProduct rebuilds a product from storage.
//
// It validates nothing, and that is deliberate: a rule tightened afterwards must
// not make existing products unreadable — including the currency rule, which a
// product priced before it existed may well break. New values come in through
// the constructor and the methods, which do validate.
func ReconstituteProduct(s ProductSnapshot) *Product {
	variants := make([]*Variant, 0, len(s.Variants))
	for i := range s.Variants {
		variants = append(variants, ReconstituteVariant(s.Variants[i]))
	}

	return &Product{
		id:          s.ID,
		name:        s.Name,
		description: s.Description,
		category:    s.Category,
		status:      s.Status,
		variants:    variants,
		createdAt:   s.CreatedAt,
		updatedAt:   s.UpdatedAt,
		version:     s.Version,
	}
}

// Snapshot returns the product's state for a repository to persist.
func (p *Product) Snapshot() ProductSnapshot {
	variants := make([]VariantSnapshot, 0, len(p.variants))
	for _, variant := range p.variants {
		variants = append(variants, variant.Snapshot())
	}

	return ProductSnapshot{
		ID:          p.id,
		Name:        p.name,
		Description: p.description,
		Category:    p.category,
		Status:      p.status,
		Variants:    variants,
		CreatedAt:   p.createdAt,
		UpdatedAt:   p.updatedAt,
		Version:     p.version,
	}
}
