package grpc_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/catalog/v1"
	commonv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/common/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/adapter/in/grpc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/port/in"
)

// stubUseCase records what the handler asked for and answers with what the test
// wants. Hand-written rather than generated because only driven ports are listed
// in .mockery.yml, and what these tests assert on is the mapping.
type stubUseCase struct {
	create        in.CreateProductCommand
	update        in.UpdateProductCommand
	addVariant    in.AddVariantCommand
	updateVariant in.UpdateVariantCommand
	query         in.ListProductsQuery
	get           in.GetProductQuery
	id            string
	ids           []string

	product *domain.Product
	page    in.ProductPage
	err     error
}

func (s *stubUseCase) GetProduct(_ context.Context, query in.GetProductQuery) (*domain.Product, error) {
	s.get = query

	return s.product, s.err
}

func (s *stubUseCase) GetProductsByIDs(_ context.Context, ids []string) ([]*domain.Product, error) {
	s.ids = ids

	return s.page.Products, s.err
}

func (s *stubUseCase) ListProducts(_ context.Context, query in.ListProductsQuery) (in.ProductPage, error) {
	s.query = query

	return s.page, s.err
}

func (s *stubUseCase) CreateProduct(_ context.Context, cmd in.CreateProductCommand) (*domain.Product, error) {
	s.create = cmd

	return s.product, s.err
}

func (s *stubUseCase) UpdateProduct(_ context.Context, cmd in.UpdateProductCommand) (*domain.Product, error) {
	s.update = cmd

	return s.product, s.err
}

func (s *stubUseCase) AddVariant(_ context.Context, cmd in.AddVariantCommand) (*domain.Product, error) {
	s.addVariant = cmd

	return s.product, s.err
}

func (s *stubUseCase) UpdateVariant(_ context.Context, cmd in.UpdateVariantCommand) (*domain.Product, error) {
	s.updateVariant = cmd

	return s.product, s.err
}

func (s *stubUseCase) PublishProduct(_ context.Context, id string) (*domain.Product, error) {
	s.id = id

	return s.product, s.err
}

func (s *stubUseCase) ArchiveProduct(_ context.Context, id string) (*domain.Product, error) {
	s.id = id

	return s.product, s.err
}

func TestCreateProductMapsRequestAndResponse(t *testing.T) {
	stub := &stubUseCase{product: storedProduct(t, domain.StatusDraft)}
	handler := adapter.NewCatalogHandler(stub)

	resp, err := handler.CreateProduct(context.Background(), &catalogv1.CreateProductRequest{
		Name:     "Oxford Shirt",
		Category: "clothing/shirts",
		Variants: []*catalogv1.NewVariant{{
			Sku:        "SHIRT-OXF-M",
			Price:      &commonv1.Money{AmountMinor: 129000, CurrencyCode: "THB"},
			Attributes: map[string]string{"size": "M"},
		}},
	})
	if err != nil {
		t.Fatalf("CreateProduct() error = %v, want nil", err)
	}

	if len(stub.create.Variants) != 1 {
		t.Fatalf("CreateProduct() passed %d variants, want 1", len(stub.create.Variants))
	}

	// The price arrives as one message and travels on as two values, which is
	// what the use case reassembles into a Money it can judge.
	variant := stub.create.Variants[0]
	if variant.PriceAmountMinor != 129000 || variant.PriceCurrency != "THB" {
		t.Errorf("CreateProduct() passed price %d %q, want 129000 THB",
			variant.PriceAmountMinor, variant.PriceCurrency)
	}

	if got := resp.GetProduct().GetStatus(); got != catalogv1.ProductStatus_PRODUCT_STATUS_DRAFT {
		t.Errorf("CreateProduct() status = %v, want draft", got)
	}

	if got := resp.GetProduct().GetVariants()[0].GetPrice().GetCurrencyCode(); got != "THB" {
		t.Errorf("CreateProduct() response currency = %q, want THB", got)
	}

	if got := resp.GetProduct().GetVariants()[0].GetAttributes()["size"]; got != "M" {
		t.Errorf("CreateProduct() response attributes = %v, want size M", resp.GetProduct().GetVariants()[0].GetAttributes())
	}
}

func TestListProductsMapsTheFilter(t *testing.T) {
	tests := []struct {
		name       string
		status     catalogv1.ProductStatus
		wantStatus domain.ProductStatus
	}{
		{
			// The default belongs to the use case, so an unspecified filter has
			// to reach it unanswered — a status chosen here would be a second
			// copy of the same rule.
			name:       "unspecified stays empty",
			status:     catalogv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED,
			wantStatus: "",
		},
		{
			name:       "draft",
			status:     catalogv1.ProductStatus_PRODUCT_STATUS_DRAFT,
			wantStatus: domain.StatusDraft,
		},
		{
			name:       "active",
			status:     catalogv1.ProductStatus_PRODUCT_STATUS_ACTIVE,
			wantStatus: domain.StatusActive,
		},
		{
			name:       "archived",
			status:     catalogv1.ProductStatus_PRODUCT_STATUS_ARCHIVED,
			wantStatus: domain.StatusArchived,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubUseCase{page: in.ProductPage{NextPageToken: "next"}}
			handler := adapter.NewCatalogHandler(stub)

			resp, err := handler.ListProducts(context.Background(), &catalogv1.ListProductsRequest{
				Category:  "clothing",
				Status:    tt.status,
				PageSize:  10,
				PageToken: "here",
			})
			if err != nil {
				t.Fatalf("ListProducts() error = %v, want nil", err)
			}

			if stub.query.Status != tt.wantStatus {
				t.Errorf("ListProducts() passed status %q, want %q", stub.query.Status, tt.wantStatus)
			}

			if stub.query.Category != "clothing" || stub.query.PageSize != 10 || stub.query.PageToken != "here" {
				t.Errorf("ListProducts() passed %+v, want the request's fields", stub.query)
			}

			if resp.GetNextPageToken() != "next" {
				t.Errorf("ListProducts() next page token = %q, want next", resp.GetNextPageToken())
			}
		})
	}
}

func TestUpdateVariantMapsBothIDsAndThePrice(t *testing.T) {
	stub := &stubUseCase{product: storedProduct(t, domain.StatusActive)}
	handler := adapter.NewCatalogHandler(stub)

	if _, err := handler.UpdateVariant(context.Background(), &catalogv1.UpdateVariantRequest{
		ProductId:  "0195c1d2-3f4a-7b8c-9d0e-1f2a3b4c5d6e",
		VariantId:  "0195c1d2-3f4a-7b8c-9d0e-1f2a3b4c5d6f",
		Price:      &commonv1.Money{AmountMinor: 99000, CurrencyCode: "THB"},
		Attributes: map[string]string{"size": "L"},
	}); err != nil {
		t.Fatalf("UpdateVariant() error = %v, want nil", err)
	}

	// Both ids travel, because a variant is only ever modified as part of the
	// product the caller was looking at.
	if stub.updateVariant.ProductID == "" || stub.updateVariant.VariantID == "" {
		t.Errorf("UpdateVariant() passed %+v, want both ids", stub.updateVariant)
	}

	if stub.updateVariant.PriceAmountMinor != 99000 || stub.updateVariant.PriceCurrency != "THB" {
		t.Errorf("UpdateVariant() passed price %d %q, want 99000 THB",
			stub.updateVariant.PriceAmountMinor, stub.updateVariant.PriceCurrency)
	}
}

func TestAddVariantReturnsTheWholeProduct(t *testing.T) {
	// A caller merging a returned fragment into a copy it already holds will
	// eventually merge it wrong.
	stub := &stubUseCase{product: storedProduct(t, domain.StatusActive)}
	handler := adapter.NewCatalogHandler(stub)

	resp, err := handler.AddVariant(context.Background(), &catalogv1.AddVariantRequest{
		ProductId: "0195c1d2-3f4a-7b8c-9d0e-1f2a3b4c5d6e",
		Variant: &catalogv1.NewVariant{
			Sku:   "SHIRT-OXF-L",
			Price: &commonv1.Money{AmountMinor: 129000, CurrencyCode: "THB"},
		},
	})
	if err != nil {
		t.Fatalf("AddVariant() error = %v, want nil", err)
	}

	if stub.addVariant.Variant.SKU != "SHIRT-OXF-L" {
		t.Errorf("AddVariant() passed sku %q, want SHIRT-OXF-L", stub.addVariant.Variant.SKU)
	}

	if len(resp.GetProduct().GetVariants()) == 0 {
		t.Error("AddVariant() returned no variants, want the whole product")
	}
}

func TestFailuresLeaveThroughToGRPC(t *testing.T) {
	// The handler classifies nothing itself: a domain refusal already declares
	// its kind, and the reason code is what a client branches on.
	tests := []struct {
		name       string
		err        error
		wantCode   codes.Code
		wantReason string
	}{
		{
			name:       "archived product",
			err:        domain.ErrProductArchived,
			wantCode:   codes.FailedPrecondition,
			wantReason: "CONFLICT",
		},
		{
			name:       "unknown variant",
			err:        domain.ErrVariantNotFound,
			wantCode:   codes.NotFound,
			wantReason: "NOT_FOUND",
		},
		{
			name:       "sku collision from the repository",
			err:        errorx.New(errorx.KindConflict, "sku is already in use").WithReason("SKU_ALREADY_EXISTS"),
			wantCode:   codes.FailedPrecondition,
			wantReason: "SKU_ALREADY_EXISTS",
		},
		{
			name:       "bad input",
			err:        domain.ValidationError{Field: "name", Message: "is required"},
			wantCode:   codes.InvalidArgument,
			wantReason: "INVALID_INPUT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := adapter.NewCatalogHandler(&stubUseCase{err: tt.err})

			_, err := handler.PublishProduct(context.Background(), &catalogv1.PublishProductRequest{
				Id: "0195c1d2-3f4a-7b8c-9d0e-1f2a3b4c5d6e",
			})
			if err == nil {
				t.Fatal("PublishProduct() error = nil, want a status")
			}

			if got := status.Code(err); got != tt.wantCode {
				t.Errorf("status.Code() = %v, want %v", got, tt.wantCode)
			}

			if got := errorx.Reason(err); got != tt.wantReason {
				t.Errorf("Reason() = %q, want %q", got, tt.wantReason)
			}
		})
	}
}

func TestStatusStoredBeforeItWasNamedIsUnspecified(t *testing.T) {
	// Reconstitute loads a row rather than refusing it, so the mapping has to
	// answer something. Inventing one of the three would tell a shopper this
	// product is buyable.
	stub := &stubUseCase{product: domain.ReconstituteProduct(domain.ProductSnapshot{
		ID:     domain.NewProductID(),
		Name:   "Oxford Shirt",
		Status: "something nobody defined",
	})}
	handler := adapter.NewCatalogHandler(stub)

	resp, err := handler.GetProduct(context.Background(), &catalogv1.GetProductRequest{
		Id: "0195c1d2-3f4a-7b8c-9d0e-1f2a3b4c5d6e",
	})
	if err != nil {
		t.Fatalf("GetProduct() error = %v, want nil", err)
	}

	if got := resp.GetProduct().GetStatus(); got != catalogv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED {
		t.Errorf("GetProduct() status = %v, want unspecified", got)
	}
}

func TestGetProductsByIDsPassesEveryID(t *testing.T) {
	stub := &stubUseCase{page: in.ProductPage{Products: []*domain.Product{storedProduct(t, domain.StatusActive)}}}
	handler := adapter.NewCatalogHandler(stub)

	ids := []string{
		"0195c1d2-3f4a-7b8c-9d0e-1f2a3b4c5d6e",
		"0195c1d2-3f4a-7b8c-9d0e-1f2a3b4c5d6f",
	}

	resp, err := handler.GetProductsByIDs(context.Background(), &catalogv1.GetProductsByIDsRequest{Ids: ids})
	if err != nil {
		t.Fatalf("GetProductsByIDs() error = %v, want nil", err)
	}

	if len(stub.ids) != len(ids) {
		t.Errorf("GetProductsByIDs() passed %d ids, want %d", len(stub.ids), len(ids))
	}

	// Fewer products than ids is the ordinary case, not an error.
	if len(resp.GetProducts()) != 1 {
		t.Errorf("GetProductsByIDs() returned %d products, want 1", len(resp.GetProducts()))
	}
}

// storedProduct builds an aggregate as it comes back from the repository.
func storedProduct(t *testing.T, status domain.ProductStatus) *domain.Product {
	t.Helper()

	price, err := domain.NewMoney(129000, "THB")
	if err != nil {
		t.Fatalf("NewMoney() error = %v", err)
	}

	id := domain.NewProductID()
	created := time.Now().UTC().Add(-time.Hour)

	return domain.ReconstituteProduct(domain.ProductSnapshot{
		ID:       id,
		Name:     "Oxford Shirt",
		Category: "clothing/shirts",
		Status:   status,
		Variants: []domain.VariantSnapshot{{
			ID:         domain.NewVariantID(),
			ProductID:  id,
			SKU:        "SHIRT-OXF-M",
			Price:      price,
			Attributes: map[string]string{"size": "M"},
			CreatedAt:  created,
			UpdatedAt:  created,
			Version:    1,
		}},
		CreatedAt: created,
		UpdatedAt: created,
		Version:   3,
	})
}
