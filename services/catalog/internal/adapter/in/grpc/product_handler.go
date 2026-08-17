package grpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/catalog/v1"
	commonv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/common/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/port/in"
)

// GetProduct reads one product with its variants.
//
// There is no authorization check, and that is a decision rather than an
// omission: this RPC is reached over east-west gRPC, and a published catalog is
// what the storefront shows everybody.
func (h *CatalogHandler) GetProduct(
	ctx context.Context,
	req *catalogv1.GetProductRequest,
) (*catalogv1.GetProductResponse, error) {
	product, err := h.products.GetProduct(ctx, req.GetId())
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &catalogv1.GetProductResponse{Product: toProto(product)}, nil
}

// GetProductsByIDs reads many, returning only the products that exist.
func (h *CatalogHandler) GetProductsByIDs(
	ctx context.Context,
	req *catalogv1.GetProductsByIDsRequest,
) (*catalogv1.GetProductsByIDsResponse, error) {
	products, err := h.products.GetProductsByIDs(ctx, req.GetIds())
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &catalogv1.GetProductsByIDsResponse{Products: toProtos(products)}, nil
}

// ListProducts pages through the catalog.
func (h *CatalogHandler) ListProducts(
	ctx context.Context,
	req *catalogv1.ListProductsRequest,
) (*catalogv1.ListProductsResponse, error) {
	page, err := h.products.ListProducts(ctx, in.ListProductsQuery{
		Category:  req.GetCategory(),
		Status:    fromProtoStatus(req.GetStatus()),
		PageSize:  int(req.GetPageSize()),
		PageToken: req.GetPageToken(),
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &catalogv1.ListProductsResponse{
		Products:      toProtos(page.Products),
		NextPageToken: page.NextPageToken,
	}, nil
}

// CreateProduct adds a draft to the catalog.
func (h *CatalogHandler) CreateProduct(
	ctx context.Context,
	req *catalogv1.CreateProductRequest,
) (*catalogv1.CreateProductResponse, error) {
	product, err := h.products.CreateProduct(ctx, in.CreateProductCommand{
		Name:        req.GetName(),
		Description: req.GetDescription(),
		Category:    req.GetCategory(),
		Variants:    fromProtoVariants(req.GetVariants()),
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &catalogv1.CreateProductResponse{Product: toProto(product)}, nil
}

// UpdateProduct rewrites a product's descriptive fields.
func (h *CatalogHandler) UpdateProduct(
	ctx context.Context,
	req *catalogv1.UpdateProductRequest,
) (*catalogv1.UpdateProductResponse, error) {
	product, err := h.products.UpdateProduct(ctx, in.UpdateProductCommand{
		ProductID:   req.GetId(),
		Name:        req.GetName(),
		Description: req.GetDescription(),
		Category:    req.GetCategory(),
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &catalogv1.UpdateProductResponse{Product: toProto(product)}, nil
}

// AddVariant adds a sellable unit to a product.
func (h *CatalogHandler) AddVariant(
	ctx context.Context,
	req *catalogv1.AddVariantRequest,
) (*catalogv1.AddVariantResponse, error) {
	product, err := h.products.AddVariant(ctx, in.AddVariantCommand{
		ProductID: req.GetProductId(),
		Variant:   fromProtoVariant(req.GetVariant()),
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &catalogv1.AddVariantResponse{Product: toProto(product)}, nil
}

// UpdateVariant reprices a variant and restates what distinguishes it.
func (h *CatalogHandler) UpdateVariant(
	ctx context.Context,
	req *catalogv1.UpdateVariantRequest,
) (*catalogv1.UpdateVariantResponse, error) {
	product, err := h.products.UpdateVariant(ctx, in.UpdateVariantCommand{
		ProductID:        req.GetProductId(),
		VariantID:        req.GetVariantId(),
		PriceAmountMinor: req.GetPrice().GetAmountMinor(),
		PriceCurrency:    req.GetPrice().GetCurrencyCode(),
		Attributes:       req.GetAttributes(),
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &catalogv1.UpdateVariantResponse{Product: toProto(product)}, nil
}

// PublishProduct moves a draft onto the storefront.
func (h *CatalogHandler) PublishProduct(
	ctx context.Context,
	req *catalogv1.PublishProductRequest,
) (*catalogv1.PublishProductResponse, error) {
	product, err := h.products.PublishProduct(ctx, req.GetId())
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &catalogv1.PublishProductResponse{Product: toProto(product)}, nil
}

// ArchiveProduct withdraws a product from sale.
func (h *CatalogHandler) ArchiveProduct(
	ctx context.Context,
	req *catalogv1.ArchiveProductRequest,
) (*catalogv1.ArchiveProductResponse, error) {
	product, err := h.products.ArchiveProduct(ctx, req.GetId())
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &catalogv1.ArchiveProductResponse{Product: toProto(product)}, nil
}

// fromProtoVariants maps the variants a create carries.
func fromProtoVariants(variants []*catalogv1.NewVariant) []in.NewVariant {
	commands := make([]in.NewVariant, 0, len(variants))
	for _, variant := range variants {
		commands = append(commands, fromProtoVariant(variant))
	}

	return commands
}

// fromProtoVariant pulls the price apart into the two values a command carries.
// It stays two until the use case, which is where an amount and a currency stop
// being separable.
func fromProtoVariant(variant *catalogv1.NewVariant) in.NewVariant {
	return in.NewVariant{
		SKU:              variant.GetSku(),
		PriceAmountMinor: variant.GetPrice().GetAmountMinor(),
		PriceCurrency:    variant.GetPrice().GetCurrencyCode(),
		Attributes:       variant.GetAttributes(),
	}
}

// fromProtoStatus maps the filter a listing carries.
//
// An unspecified status becomes the empty one rather than a default chosen here:
// what the storefront sees when it does not ask is a decision the use case owns,
// and a driving adapter that answered it would be a second place holding the
// same rule.
func fromProtoStatus(status catalogv1.ProductStatus) domain.ProductStatus {
	switch status {
	case catalogv1.ProductStatus_PRODUCT_STATUS_DRAFT:
		return domain.StatusDraft
	case catalogv1.ProductStatus_PRODUCT_STATUS_ACTIVE:
		return domain.StatusActive
	case catalogv1.ProductStatus_PRODUCT_STATUS_ARCHIVED:
		return domain.StatusArchived
	case catalogv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

// toProtoStatus maps the lifecycle state outward.
func toProtoStatus(status domain.ProductStatus) catalogv1.ProductStatus {
	switch status {
	case domain.StatusDraft:
		return catalogv1.ProductStatus_PRODUCT_STATUS_DRAFT
	case domain.StatusActive:
		return catalogv1.ProductStatus_PRODUCT_STATUS_ACTIVE
	case domain.StatusArchived:
		return catalogv1.ProductStatus_PRODUCT_STATUS_ARCHIVED
	default:
		// A row stored before a state was named, which Reconstitute loads
		// rather than refuses. Unspecified is the honest answer: inventing one
		// of the three would tell a shopper this product is buyable.
		return catalogv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED
	}
}

// toProtos maps a page of aggregates.
func toProtos(products []*domain.Product) []*catalogv1.Product {
	messages := make([]*catalogv1.Product, 0, len(products))
	for _, product := range products {
		messages = append(messages, toProto(product))
	}

	return messages
}

// toProto maps the aggregate to what this service tells everyone else.
//
// The version is dropped here, deliberately: a field on
// ecommerce.catalog.v1.Product is a promise to every caller, and the optimistic
// lock is not a fact anyone outside should be able to depend on.
func toProto(product *domain.Product) *catalogv1.Product {
	variants := make([]*catalogv1.Variant, 0, len(product.Variants()))

	for _, variant := range product.Variants() {
		variants = append(variants, &catalogv1.Variant{
			Id:        variant.ID().String(),
			ProductId: variant.ProductID().String(),
			Sku:       variant.SKU().String(),
			Price: &commonv1.Money{
				AmountMinor:  variant.Price().AmountMinor(),
				CurrencyCode: variant.Price().Currency().String(),
			},
			Attributes: variant.Attributes(),
			CreatedAt:  timestamppb.New(variant.CreatedAt()),
			UpdatedAt:  timestamppb.New(variant.UpdatedAt()),
		})
	}

	return &catalogv1.Product{
		Id:          product.ID().String(),
		Name:        product.Name(),
		Description: product.Description(),
		Category:    product.Category().String(),
		Status:      toProtoStatus(product.Status()),
		Variants:    variants,
		CreatedAt:   timestamppb.New(product.CreatedAt()),
		UpdatedAt:   timestamppb.New(product.UpdatedAt()),
	}
}
