// Package app holds the use cases: the orchestration between a request and the
// domain rules that answer it. There is deliberately very little here — an `if`
// in this package that encodes a business policy is a rule that escaped the
// domain, where it could have been tested without a mock.
package app

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/port/out"
)

// What a listing returns when the caller does not say. The upper bound is the
// proto's to enforce; it is repeated here as a ceiling for callers that are not
// gRPC, since the cost of an unbounded page is paid by this process.
const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// ProductService implements the catalog use cases over a repository.
type ProductService struct {
	products out.ProductRepository
	tx       out.TxManager
}

// Compile-time proof that the driving port is satisfied. Without it the failure
// surfaces in bootstrap, naming the wiring rather than the missing method.
var _ in.ProductUseCase = (*ProductService)(nil)

// NewProductService wires the use cases to their driven ports.
func NewProductService(products out.ProductRepository, tx out.TxManager) *ProductService {
	return &ProductService{products: products, tx: tx}
}

// GetProduct reads one product with its variants, in the state the query asked
// about and not found in any other.
//
// The state is checked here rather than in the query the repository runs because
// there is no page to narrow: the row is fetched by primary key either way, and
// a second WHERE clause would only move the same comparison into SQL.
func (s *ProductService) GetProduct(ctx context.Context, query in.GetProductQuery) (*domain.Product, error) {
	productID, err := domain.ParseProductID(query.ID)
	if err != nil {
		return nil, err
	}

	status := query.Status
	if status == "" {
		// The storefront is the caller, and of the two ways to be wrong here,
		// handing a shopper an unfinished draft is the one nobody notices until
		// it is on a screen.
		status = domain.StatusActive
	}

	product, err := s.products.FindByID(ctx, productID)
	if err != nil {
		return nil, err
	}

	if product.Status() != status {
		// Not found and not forbidden: whether a draft exists under this id is
		// itself something a caller asking about published products does not
		// get to learn, and the two answers are distinguishable.
		return nil, errorx.New(errorx.KindNotFound, "product %s not found", query.ID).
			WithReason("PRODUCT_NOT_FOUND")
	}

	return product, nil
}

// GetProductsByIDs reads many.
//
// One malformed id fails the whole call, where one *missing* id does not: the
// first is a bad request, the second is the ordinary case of a screen naming a
// product that has since been archived.
func (s *ProductService) GetProductsByIDs(ctx context.Context, ids []string) ([]*domain.Product, error) {
	productIDs := make([]domain.ProductID, 0, len(ids))

	for _, id := range ids {
		productID, err := domain.ParseProductID(id)
		if err != nil {
			return nil, err
		}

		productIDs = append(productIDs, productID)
	}

	return s.products.FindByIDs(ctx, productIDs)
}

// ListProducts returns one page of the catalog.
//
// The repository is asked for one row more than the page holds, which is how the
// next token is decided without a second query counting rows nobody will read.
func (s *ProductService) ListProducts(ctx context.Context, query in.ListProductsQuery) (in.ProductPage, error) {
	category, err := domain.NewCategory(query.Category)
	if err != nil {
		return in.ProductPage{}, err
	}

	after, err := decodeCursor(query.PageToken)
	if err != nil {
		return in.ProductPage{}, err
	}

	status := query.Status
	if status == "" {
		// The storefront is the caller, and of the two ways to be wrong here,
		// showing a shopper an unfinished draft is the one nobody notices until
		// it is on a screen.
		status = domain.StatusActive
	}

	size := pageSize(query.PageSize)

	products, err := s.products.List(ctx, out.ProductFilter{
		Status:   status,
		Category: category,
		After:    after,
		Limit:    size + 1,
	})
	if err != nil {
		return in.ProductPage{}, err
	}

	if len(products) <= size {
		return in.ProductPage{Products: products}, nil
	}

	products = products[:size]
	last := products[size-1]

	return in.ProductPage{
		Products:      products,
		NextPageToken: encodeCursor(out.ProductCursor{CreatedAt: last.CreatedAt(), ID: last.ID()}),
	}, nil
}

// CreateProduct adds a draft to the catalog.
//
// The variants are added to the aggregate before anything is written, so a
// command carrying two of one SKU is refused by the product itself rather than
// by a constraint half way through an insert.
func (s *ProductService) CreateProduct(
	ctx context.Context,
	cmd in.CreateProductCommand,
) (*domain.Product, error) {
	category, err := domain.NewCategory(cmd.Category)
	if err != nil {
		return nil, err
	}

	product, err := domain.NewProduct(cmd.Name, cmd.Description, category)
	if err != nil {
		return nil, err
	}

	for _, variant := range cmd.Variants {
		sku, price, err := newVariant(variant)
		if err != nil {
			return nil, err
		}

		if err := product.AddVariant(sku, price, variant.Attributes); err != nil {
			return nil, err
		}
	}

	var created *domain.Product

	// One transaction for what reads as one insert: the aggregate is a product
	// row and its variant rows, and a product that arrived without the variants
	// it was created with is not a state this service has a name for.
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		var err error

		created, err = s.products.Create(ctx, product)

		return err
	})
	if err != nil {
		return nil, err
	}

	return created, nil
}

// UpdateProduct rewrites a product's descriptive fields.
func (s *ProductService) UpdateProduct(
	ctx context.Context,
	cmd in.UpdateProductCommand,
) (*domain.Product, error) {
	category, err := domain.NewCategory(cmd.Category)
	if err != nil {
		return nil, err
	}

	return s.mutate(ctx, cmd.ProductID, func(product *domain.Product) error {
		return product.UpdateDetails(cmd.Name, cmd.Description, category)
	})
}

// AddVariant adds a sellable unit to an existing product.
func (s *ProductService) AddVariant(ctx context.Context, cmd in.AddVariantCommand) (*domain.Product, error) {
	sku, price, err := newVariant(cmd.Variant)
	if err != nil {
		return nil, err
	}

	return s.mutate(ctx, cmd.ProductID, func(product *domain.Product) error {
		return product.AddVariant(sku, price, cmd.Variant.Attributes)
	})
}

// UpdateVariant reprices one and restates what distinguishes it.
func (s *ProductService) UpdateVariant(
	ctx context.Context,
	cmd in.UpdateVariantCommand,
) (*domain.Product, error) {
	variantID, err := domain.ParseVariantID(cmd.VariantID)
	if err != nil {
		return nil, err
	}

	price, err := newMoney(cmd.PriceAmountMinor, cmd.PriceCurrency)
	if err != nil {
		return nil, err
	}

	return s.mutate(ctx, cmd.ProductID, func(product *domain.Product) error {
		return product.UpdateVariant(variantID, price, cmd.Attributes)
	})
}

// PublishProduct moves a draft onto the storefront.
func (s *ProductService) PublishProduct(ctx context.Context, id string) (*domain.Product, error) {
	return s.mutate(ctx, id, (*domain.Product).Publish)
}

// ArchiveProduct withdraws a product from sale.
func (s *ProductService) ArchiveProduct(ctx context.Context, id string) (*domain.Product, error) {
	return s.mutate(ctx, id, func(product *domain.Product) error {
		product.Archive()

		return nil
	})
}

// mutate loads an aggregate, hands it to change, and writes it back.
//
// Both halves are inside one transaction because the optimistic lock is only
// worth anything if they are: a version read on its own may already have moved
// by the time the update carrying it is sent, and the update would then win
// where it should have been told to re-read.
//
// Everything a caller got wrong is rejected before the transaction opens, which
// is why change takes no raw input — a malformed SKU must not cost a BEGIN.
func (s *ProductService) mutate(
	ctx context.Context,
	id string,
	change func(product *domain.Product) error,
) (*domain.Product, error) {
	productID, err := domain.ParseProductID(id)
	if err != nil {
		return nil, err
	}

	var updated *domain.Product

	err = s.tx.Do(ctx, func(ctx context.Context) error {
		product, err := s.products.FindByID(ctx, productID)
		if err != nil {
			return err
		}

		if err := change(product); err != nil {
			return err
		}

		updated, err = s.products.Update(ctx, product)

		return err
	})
	if err != nil {
		return nil, err
	}

	return updated, nil
}

// newVariant turns the raw values a caller sent into the domain types the
// aggregate accepts. It judges nothing itself — whether a variant may be added
// at all is the product's answer.
func newVariant(variant in.NewVariant) (domain.SKU, domain.Money, error) {
	sku, err := domain.NewSKU(variant.SKU)
	if err != nil {
		return "", domain.Money{}, err
	}

	price, err := newMoney(variant.PriceAmountMinor, variant.PriceCurrency)
	if err != nil {
		return "", domain.Money{}, err
	}

	return sku, price, nil
}

// newMoney reassembles a price from the two halves the wire carries it in.
func newMoney(amountMinor int64, currency string) (domain.Money, error) {
	code, err := domain.NewCurrencyCode(currency)
	if err != nil {
		return domain.Money{}, err
	}

	return domain.NewMoney(amountMinor, code)
}

// pageSize applies the default and the ceiling.
func pageSize(requested int) int {
	switch {
	case requested <= 0:
		return defaultPageSize
	case requested > maxPageSize:
		return maxPageSize
	default:
		return requested
	}
}
