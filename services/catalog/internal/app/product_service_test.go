package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/app"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/port/out"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/port/out/mocks"
)

// The mocks assert their own expectations on cleanup, so a call that was set up
// and never made fails the test — which is how "no transaction was opened"
// below is checked without asserting on a counter.
func setup(t *testing.T) (*mocks.MockProductRepository, *mocks.MockTxManager, *app.ProductService) {
	t.Helper()

	products := mocks.NewMockProductRepository(t)
	tx := mocks.NewMockTxManager(t)

	return products, tx, app.NewProductService(products, tx)
}

// expectTx runs the use case's transactional work inline, so the assertions
// below are about what happened inside one and not about txmanager.
func expectTx(tx *mocks.MockTxManager) {
	tx.EXPECT().
		Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).
		Once()
}

func TestCreateProduct(t *testing.T) {
	t.Run("builds the whole aggregate before anything is written", func(t *testing.T) {
		products, tx, service := setup(t)
		expectTx(tx)

		products.EXPECT().
			Create(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, product *domain.Product) (*domain.Product, error) {
				if got := product.Category(); got != "clothing/shirts" {
					t.Errorf("Create() got category %q, want the normalised form", got)
				}

				if got := product.Status(); got != domain.StatusDraft {
					t.Errorf("Create() got status %q, want %q", got, domain.StatusDraft)
				}

				if got := len(product.Variants()); got != 2 {
					t.Fatalf("Create() got %d variants, want 2", got)
				}

				if got := product.Variants()[0].SKU(); got != "SHIRT-OXF-M" {
					t.Errorf("Create() got sku %q, want the uppercased form", got)
				}

				return product, nil
			}).
			Once()

		product, err := service.CreateProduct(context.Background(), in.CreateProductCommand{
			Name:     "Oxford Shirt",
			Category: "Clothing/Shirts",
			Variants: []in.NewVariant{
				{SKU: "shirt-oxf-m", PriceAmountMinor: 129000, PriceCurrency: "thb"},
				{SKU: "shirt-oxf-l", PriceAmountMinor: 129000, PriceCurrency: "THB"},
			},
		})
		if err != nil {
			t.Fatalf("CreateProduct() error = %v, want nil", err)
		}

		if product.Name() != "Oxford Shirt" {
			t.Errorf("CreateProduct() name = %q, want %q", product.Name(), "Oxford Shirt")
		}
	})

	t.Run("a command carrying two of one sku is refused by the aggregate", func(t *testing.T) {
		// Neither mock is given an expectation, so any call fails: the product
		// says no before a transaction is opened, rather than a UNIQUE
		// constraint saying so half way through an insert.
		_, _, service := setup(t)

		_, err := service.CreateProduct(context.Background(), in.CreateProductCommand{
			Name: "Oxford Shirt",
			Variants: []in.NewVariant{
				{SKU: "SHIRT-OXF-M", PriceAmountMinor: 129000, PriceCurrency: "THB"},
				{SKU: "SHIRT-OXF-M", PriceAmountMinor: 139000, PriceCurrency: "THB"},
			},
		})

		if !errors.Is(err, domain.ErrDuplicateSKU) {
			t.Errorf("CreateProduct() error = %v, want %v", err, domain.ErrDuplicateSKU)
		}
	})

	t.Run("variants priced in two currencies are refused", func(t *testing.T) {
		_, _, service := setup(t)

		_, err := service.CreateProduct(context.Background(), in.CreateProductCommand{
			Name: "Oxford Shirt",
			Variants: []in.NewVariant{
				{SKU: "SHIRT-OXF-M", PriceAmountMinor: 129000, PriceCurrency: "THB"},
				{SKU: "SHIRT-OXF-L", PriceAmountMinor: 4900, PriceCurrency: "USD"},
			},
		})

		if !errors.Is(err, domain.ErrCurrencyMismatch) {
			t.Errorf("CreateProduct() error = %v, want %v", err, domain.ErrCurrencyMismatch)
		}
	})

	t.Run("a malformed sku costs no transaction", func(t *testing.T) {
		_, _, service := setup(t)

		_, err := service.CreateProduct(context.Background(), in.CreateProductCommand{
			Name:     "Oxford Shirt",
			Variants: []in.NewVariant{{SKU: "!!", PriceAmountMinor: 1, PriceCurrency: "THB"}},
		})

		if got := errorx.KindOf(err); got != errorx.KindInvalidInput {
			t.Errorf("KindOf() = %q, want %q", got, errorx.KindInvalidInput)
		}
	})
}

func TestGetProduct(t *testing.T) {
	t.Run("reads the product it was asked for", func(t *testing.T) {
		products, _, service := setup(t)
		stored := storedProduct(t, domain.StatusActive)

		products.EXPECT().FindByID(mock.Anything, stored.ID()).Return(stored, nil).Once()

		got, err := service.GetProduct(context.Background(), stored.ID().String())
		if err != nil {
			t.Fatalf("GetProduct() error = %v, want nil", err)
		}

		if got.ID() != stored.ID() {
			t.Errorf("GetProduct() id = %q, want %q", got.ID(), stored.ID())
		}
	})

	t.Run("a malformed id never reaches the repository", func(t *testing.T) {
		// An id that reaches pgx unchecked becomes a parse failure reported as
		// Internal, which says the service is broken when the caller made a
		// typo.
		_, _, service := setup(t)

		_, err := service.GetProduct(context.Background(), "not-a-uuid")

		if got := errorx.KindOf(err); got != errorx.KindInvalidInput {
			t.Errorf("KindOf() = %q, want %q", got, errorx.KindInvalidInput)
		}
	})
}

func TestGetProductsByIDs(t *testing.T) {
	t.Run("one malformed id fails the whole call", func(t *testing.T) {
		_, _, service := setup(t)

		_, err := service.GetProductsByIDs(context.Background(), []string{
			domain.NewProductID().String(),
			"not-a-uuid",
		})

		if got := errorx.KindOf(err); got != errorx.KindInvalidInput {
			t.Errorf("KindOf() = %q, want %q", got, errorx.KindInvalidInput)
		}
	})

	t.Run("a missing id is not an error", func(t *testing.T) {
		// This is the read a BFF fans out to fill a screen, and one archived
		// product must not fail the screen.
		products, _, service := setup(t)
		stored := storedProduct(t, domain.StatusActive)
		missing := domain.NewProductID()

		products.EXPECT().
			FindByIDs(mock.Anything, []domain.ProductID{stored.ID(), missing}).
			Return([]*domain.Product{stored}, nil).
			Once()

		got, err := service.GetProductsByIDs(context.Background(), []string{
			stored.ID().String(),
			missing.String(),
		})
		if err != nil {
			t.Fatalf("GetProductsByIDs() error = %v, want nil", err)
		}

		if len(got) != 1 {
			t.Errorf("GetProductsByIDs() returned %d products, want 1", len(got))
		}
	})
}

func TestListProducts(t *testing.T) {
	t.Run("asks for one more row than the page holds", func(t *testing.T) {
		products, _, service := setup(t)
		page := []*domain.Product{
			storedProduct(t, domain.StatusActive),
			storedProduct(t, domain.StatusActive),
			storedProduct(t, domain.StatusActive),
		}

		var filter out.ProductFilter

		products.EXPECT().
			List(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, f out.ProductFilter) ([]*domain.Product, error) {
				filter = f

				return page, nil
			}).
			Once()

		got, err := service.ListProducts(context.Background(), in.ListProductsQuery{PageSize: 2})
		if err != nil {
			t.Fatalf("ListProducts() error = %v, want nil", err)
		}

		if filter.Limit != 3 {
			t.Errorf("List() limit = %d, want 3", filter.Limit)
		}

		if len(got.Products) != 2 {
			t.Errorf("ListProducts() returned %d products, want 2", len(got.Products))
		}

		if got.NextPageToken == "" {
			t.Error("ListProducts() next page token is empty, want the extra row to have produced one")
		}
	})

	t.Run("the last page carries no token", func(t *testing.T) {
		products, _, service := setup(t)

		products.EXPECT().
			List(mock.Anything, mock.Anything).
			Return([]*domain.Product{storedProduct(t, domain.StatusActive)}, nil).
			Once()

		got, err := service.ListProducts(context.Background(), in.ListProductsQuery{PageSize: 2})
		if err != nil {
			t.Fatalf("ListProducts() error = %v, want nil", err)
		}

		if got.NextPageToken != "" {
			t.Errorf("ListProducts() next page token = %q, want empty", got.NextPageToken)
		}
	})

	t.Run("the next page resumes where the last one stopped", func(t *testing.T) {
		products, _, service := setup(t)
		first := storedProduct(t, domain.StatusActive)
		page := []*domain.Product{first, storedProduct(t, domain.StatusActive)}

		products.EXPECT().List(mock.Anything, mock.Anything).Return(page, nil).Once()

		got, err := service.ListProducts(context.Background(), in.ListProductsQuery{PageSize: 1})
		if err != nil {
			t.Fatalf("ListProducts() error = %v, want nil", err)
		}

		var filter out.ProductFilter

		products.EXPECT().
			List(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, f out.ProductFilter) ([]*domain.Product, error) {
				filter = f

				return nil, nil
			}).
			Once()

		if _, err := service.ListProducts(context.Background(), in.ListProductsQuery{
			PageSize:  1,
			PageToken: got.NextPageToken,
		}); err != nil {
			t.Fatalf("ListProducts() error = %v, want nil", err)
		}

		if filter.After == nil {
			t.Fatal("List() cursor is nil, want the end of the first page")
		}

		// Both halves matter: created_at alone cannot separate two products
		// written in the same transaction.
		if filter.After.ID != first.ID() || !filter.After.CreatedAt.Equal(first.CreatedAt()) {
			t.Errorf("List() cursor = %+v, want %q at %v", filter.After, first.ID(), first.CreatedAt())
		}
	})

	t.Run("an unset status lists what can be sold", func(t *testing.T) {
		products, _, service := setup(t)

		var filter out.ProductFilter

		products.EXPECT().
			List(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, f out.ProductFilter) ([]*domain.Product, error) {
				filter = f

				return nil, nil
			}).
			Once()

		if _, err := service.ListProducts(context.Background(), in.ListProductsQuery{}); err != nil {
			t.Fatalf("ListProducts() error = %v, want nil", err)
		}

		if filter.Status != domain.StatusActive {
			t.Errorf("List() status = %q, want %q", filter.Status, domain.StatusActive)
		}
	})

	t.Run("page size is defaulted and capped", func(t *testing.T) {
		tests := []struct {
			name      string
			requested int
			wantLimit int
		}{
			{name: "unset", requested: 0, wantLimit: 21},
			{name: "negative", requested: -5, wantLimit: 21},
			{name: "within bounds", requested: 50, wantLimit: 51},
			{name: "over the ceiling", requested: 5000, wantLimit: 101},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				products, _, service := setup(t)

				var filter out.ProductFilter

				products.EXPECT().
					List(mock.Anything, mock.Anything).
					RunAndReturn(func(_ context.Context, f out.ProductFilter) ([]*domain.Product, error) {
						filter = f

						return nil, nil
					}).
					Once()

				if _, err := service.ListProducts(context.Background(), in.ListProductsQuery{
					PageSize: tt.requested,
				}); err != nil {
					t.Fatalf("ListProducts() error = %v, want nil", err)
				}

				if filter.Limit != tt.wantLimit {
					t.Errorf("List() limit = %d, want %d", filter.Limit, tt.wantLimit)
				}
			})
		}
	})

	t.Run("a token this service did not issue is invalid input", func(t *testing.T) {
		// Not Internal: a page token is the one input on this call that arrives
		// mangled by a URL more often than by a bug.
		_, _, service := setup(t)

		tokens := map[string]string{
			"not base64":         "not-base64!",
			"no separator":       "bm90LWEtY3Vyc29y",
			"unparseable time":   "MjAyNi0wOC0xN3xub3QtYS11dWlk",
			"unparseable id":     "MjAyNi0wOC0xN1QwMDowMDowMFp8bm90LWEtdXVpZA",
			"empty after decode": "fA",
		}

		for _, token := range tokens {
			_, err := service.ListProducts(context.Background(), in.ListProductsQuery{PageToken: token})

			if got := errorx.KindOf(err); got != errorx.KindInvalidInput {
				t.Errorf("KindOf(%q) = %q, want %q", token, got, errorx.KindInvalidInput)
			}
		}
	})
}

func TestAddVariant(t *testing.T) {
	t.Run("reads, changes, and writes inside one transaction", func(t *testing.T) {
		products, tx, service := setup(t)
		stored := storedProduct(t, domain.StatusActive)
		expectTx(tx)

		products.EXPECT().FindByID(mock.Anything, stored.ID()).Return(stored, nil).Once()
		products.EXPECT().
			Update(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, product *domain.Product) (*domain.Product, error) {
				if got := len(product.Variants()); got != 2 {
					t.Errorf("Update() got %d variants, want 2", got)
				}

				return product, nil
			}).
			Once()

		if _, err := service.AddVariant(context.Background(), in.AddVariantCommand{
			ProductID: stored.ID().String(),
			Variant: in.NewVariant{
				SKU:              "SHIRT-OXF-L",
				PriceAmountMinor: 129000,
				PriceCurrency:    "THB",
			},
		}); err != nil {
			t.Fatalf("AddVariant() error = %v, want nil", err)
		}
	})

	t.Run("a refusal from the aggregate writes nothing", func(t *testing.T) {
		// Update is given no expectation, so the transaction rolling back is
		// checked by the write never being attempted rather than by asserting
		// on the rollback.
		products, tx, service := setup(t)
		stored := storedProduct(t, domain.StatusArchived)
		expectTx(tx)

		products.EXPECT().FindByID(mock.Anything, stored.ID()).Return(stored, nil).Once()

		_, err := service.AddVariant(context.Background(), in.AddVariantCommand{
			ProductID: stored.ID().String(),
			Variant: in.NewVariant{
				SKU:              "SHIRT-OXF-L",
				PriceAmountMinor: 129000,
				PriceCurrency:    "THB",
			},
		})

		if !errors.Is(err, domain.ErrProductArchived) {
			t.Errorf("AddVariant() error = %v, want %v", err, domain.ErrProductArchived)
		}
	})

	t.Run("a malformed price never opens a transaction", func(t *testing.T) {
		_, _, service := setup(t)

		_, err := service.AddVariant(context.Background(), in.AddVariantCommand{
			ProductID: domain.NewProductID().String(),
			Variant: in.NewVariant{
				SKU:              "SHIRT-OXF-L",
				PriceAmountMinor: -1,
				PriceCurrency:    "THB",
			},
		})

		if got := errorx.KindOf(err); got != errorx.KindInvalidInput {
			t.Errorf("KindOf() = %q, want %q", got, errorx.KindInvalidInput)
		}
	})
}

func TestUpdateVariant(t *testing.T) {
	t.Run("reprices the variant it names", func(t *testing.T) {
		products, tx, service := setup(t)
		stored := storedProduct(t, domain.StatusActive)
		variantID := stored.Variants()[0].ID()
		expectTx(tx)

		products.EXPECT().FindByID(mock.Anything, stored.ID()).Return(stored, nil).Once()
		products.EXPECT().
			Update(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, product *domain.Product) (*domain.Product, error) {
				if got := product.Variants()[0].Price().AmountMinor(); got != 99000 {
					t.Errorf("Update() got price %d, want 99000", got)
				}

				return product, nil
			}).
			Once()

		if _, err := service.UpdateVariant(context.Background(), in.UpdateVariantCommand{
			ProductID:        stored.ID().String(),
			VariantID:        variantID.String(),
			PriceAmountMinor: 99000,
			PriceCurrency:    "THB",
		}); err != nil {
			t.Fatalf("UpdateVariant() error = %v, want nil", err)
		}
	})

	t.Run("a malformed variant id never opens a transaction", func(t *testing.T) {
		_, _, service := setup(t)

		_, err := service.UpdateVariant(context.Background(), in.UpdateVariantCommand{
			ProductID:        domain.NewProductID().String(),
			VariantID:        "not-a-uuid",
			PriceAmountMinor: 99000,
			PriceCurrency:    "THB",
		})

		if got := errorx.KindOf(err); got != errorx.KindInvalidInput {
			t.Errorf("KindOf() = %q, want %q", got, errorx.KindInvalidInput)
		}
	})
}

func TestUpdateProduct(t *testing.T) {
	t.Run("normalises the category before the aggregate sees it", func(t *testing.T) {
		products, tx, service := setup(t)
		stored := storedProduct(t, domain.StatusDraft)
		expectTx(tx)

		products.EXPECT().FindByID(mock.Anything, stored.ID()).Return(stored, nil).Once()
		products.EXPECT().
			Update(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, product *domain.Product) (*domain.Product, error) {
				if got := product.Category(); got != "food/grains" {
					t.Errorf("Update() got category %q, want the normalised form", got)
				}

				return product, nil
			}).
			Once()

		if _, err := service.UpdateProduct(context.Background(), in.UpdateProductCommand{
			ProductID: stored.ID().String(),
			Name:      "Rice",
			Category:  "Food/Grains",
		}); err != nil {
			t.Fatalf("UpdateProduct() error = %v, want nil", err)
		}
	})

	t.Run("a malformed category never opens a transaction", func(t *testing.T) {
		_, _, service := setup(t)

		_, err := service.UpdateProduct(context.Background(), in.UpdateProductCommand{
			ProductID: domain.NewProductID().String(),
			Name:      "Rice",
			Category:  "food//grains",
		})

		if got := errorx.KindOf(err); got != errorx.KindInvalidInput {
			t.Errorf("KindOf() = %q, want %q", got, errorx.KindInvalidInput)
		}
	})
}

func TestPublishProduct(t *testing.T) {
	t.Run("moves a draft onto the storefront", func(t *testing.T) {
		products, tx, service := setup(t)
		stored := storedProduct(t, domain.StatusDraft)
		expectTx(tx)

		products.EXPECT().FindByID(mock.Anything, stored.ID()).Return(stored, nil).Once()
		products.EXPECT().
			Update(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, product *domain.Product) (*domain.Product, error) {
				if got := product.Status(); got != domain.StatusActive {
					t.Errorf("Update() got status %q, want %q", got, domain.StatusActive)
				}

				return product, nil
			}).
			Once()

		if _, err := service.PublishProduct(context.Background(), stored.ID().String()); err != nil {
			t.Fatalf("PublishProduct() error = %v, want nil", err)
		}
	})

	t.Run("a product with nothing to sell is not written", func(t *testing.T) {
		products, tx, service := setup(t)
		stored := domain.ReconstituteProduct(domain.ProductSnapshot{
			ID:        domain.NewProductID(),
			Name:      "Oxford Shirt",
			Status:    domain.StatusDraft,
			CreatedAt: time.Now(),
			Version:   1,
		})
		expectTx(tx)

		products.EXPECT().FindByID(mock.Anything, stored.ID()).Return(stored, nil).Once()

		_, err := service.PublishProduct(context.Background(), stored.ID().String())

		if !errors.Is(err, domain.ErrProductHasNoVariants) {
			t.Errorf("PublishProduct() error = %v, want %v", err, domain.ErrProductHasNoVariants)
		}
	})
}

func TestArchiveProduct(t *testing.T) {
	products, tx, service := setup(t)
	stored := storedProduct(t, domain.StatusActive)
	expectTx(tx)

	products.EXPECT().FindByID(mock.Anything, stored.ID()).Return(stored, nil).Once()
	products.EXPECT().
		Update(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, product *domain.Product) (*domain.Product, error) {
			if got := product.Status(); got != domain.StatusArchived {
				t.Errorf("Update() got status %q, want %q", got, domain.StatusArchived)
			}

			return product, nil
		}).
		Once()

	if _, err := service.ArchiveProduct(context.Background(), stored.ID().String()); err != nil {
		t.Fatalf("ArchiveProduct() error = %v, want nil", err)
	}
}

// storedProduct builds an aggregate as it comes back from the repository: with
// one THB variant, and with the timestamps and version only a stored row has.
func storedProduct(t *testing.T, status domain.ProductStatus) *domain.Product {
	t.Helper()

	price, err := domain.NewMoney(129000, "THB")
	if err != nil {
		t.Fatalf("NewMoney() = %v, want no error", err)
	}

	id := domain.NewProductID()
	created := time.Now().UTC().Add(-time.Hour)

	return domain.ReconstituteProduct(domain.ProductSnapshot{
		ID:       id,
		Name:     "Oxford Shirt",
		Category: "clothing/shirts",
		Status:   status,
		Variants: []domain.VariantSnapshot{{
			ID:        domain.NewVariantID(),
			ProductID: id,
			SKU:       "SHIRT-OXF-M",
			Price:     price,
			CreatedAt: created,
			UpdatedAt: created,
			Version:   1,
		}},
		CreatedAt: created,
		UpdatedAt: created,
		Version:   3,
	})
}
