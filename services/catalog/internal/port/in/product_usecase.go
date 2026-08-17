// Package in declares the driving ports: what can be asked of this service,
// stated without reference to how the asking arrives. A gRPC handler maps its
// request into one of the commands below and calls the interface.
package in

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/domain"
)

// NewVariant is a sellable unit a caller wants to exist.
//
// The fields are the raw values that arrived, not domain types: uppercasing a
// SKU and refusing a negative price are rules, and rules are not the adapter's
// to apply.
type NewVariant struct {
	SKU string

	// The price, split the way the wire carries it. It becomes a domain.Money
	// in the use case, which is where the two halves stop being separable.
	PriceAmountMinor int64
	PriceCurrency    string

	Attributes map[string]string
}

// CreateProductCommand is a request to add a product to the catalog.
type CreateProductCommand struct {
	Name        string
	Description string
	Category    string

	// May be empty. The product is created as a draft either way, and a draft
	// with no variants is a product nobody has priced yet rather than an error.
	Variants []NewVariant
}

// UpdateProductCommand replaces everything about a product that is description
// rather than commerce. Every field is written, including the empty ones.
type UpdateProductCommand struct {
	ProductID   string
	Name        string
	Description string
	Category    string
}

// AddVariantCommand is a request to add a sellable unit to an existing product.
type AddVariantCommand struct {
	ProductID string
	Variant   NewVariant
}

// UpdateVariantCommand reprices a variant and restates what distinguishes it.
//
// It names the product as well as the variant, because a variant is only ever
// modified as part of its aggregate — passing both is what stops a caller
// repricing something under a product they were not looking at.
type UpdateVariantCommand struct {
	ProductID string
	VariantID string

	PriceAmountMinor int64
	PriceCurrency    string

	Attributes map[string]string
}

// ListProductsQuery is one page of a browse.
type ListProductsQuery struct {
	// Category filters on an exact slug, or every category when empty.
	Category string

	// Status filters on exactly one state, never "any", and empty means the
	// default rather than "everything" — a listing that fell back to no filter
	// would put an unfinished draft on a storefront.
	Status domain.ProductStatus

	// PageSize of zero means the default. The upper bound is the proto's to
	// enforce; what is here is a floor of sanity for callers that are not gRPC.
	PageSize int

	// PageToken is a NextPageToken from an earlier page, opaque to everyone
	// outside this service, and empty for the first page.
	PageToken string
}

// ProductPage is a page of results and the way to ask for the next one.
type ProductPage struct {
	Products []*domain.Product

	// NextPageToken is empty on the last page. It encodes where the page
	// stopped rather than how far in it was, so products inserted while a
	// shopper reads are neither skipped nor repeated.
	NextPageToken string
}

// ProductUseCase is everything that can be done to the catalog.
//
// One interface rather than one per use case, and reads alongside writes,
// because they share a lifetime and a dependency set — a repository and a
// transaction. It splits the day some of it needs a search engine or a cache
// that the rest has no use for.
type ProductUseCase interface {
	// GetProduct reads one product with its variants, and reports not found
	// rather than nil.
	GetProduct(ctx context.Context, id string) (*domain.Product, error)

	// GetProductsByIDs reads many, returning only the ones that exist. This is
	// the read a BFF fans out to fill a screen, so one archived product must not
	// fail the screen.
	GetProductsByIDs(ctx context.Context, ids []string) ([]*domain.Product, error)

	// ListProducts pages through the catalog, newest first.
	ListProducts(ctx context.Context, query ListProductsQuery) (ProductPage, error)

	// CreateProduct adds a draft, optionally with variants.
	CreateProduct(ctx context.Context, cmd CreateProductCommand) (*domain.Product, error)

	// UpdateProduct rewrites a product's descriptive fields.
	UpdateProduct(ctx context.Context, cmd UpdateProductCommand) (*domain.Product, error)

	// AddVariant adds a sellable unit, and returns the whole product: adding a
	// variant is a change to the product, and a caller merging a fragment into
	// a copy it already holds will eventually merge it wrong.
	AddVariant(ctx context.Context, cmd AddVariantCommand) (*domain.Product, error)

	// UpdateVariant reprices one, and returns the whole product for the same
	// reason.
	UpdateVariant(ctx context.Context, cmd UpdateVariantCommand) (*domain.Product, error)

	// PublishProduct moves a draft onto the storefront, and succeeds against a
	// product that is already there.
	PublishProduct(ctx context.Context, id string) (*domain.Product, error)

	// ArchiveProduct withdraws a product from sale. Nothing is deleted and the
	// move is one-way.
	ArchiveProduct(ctx context.Context, id string) (*domain.Product, error)
}
