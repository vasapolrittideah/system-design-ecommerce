// Package grpc is the driving adapter: it maps ecommerce.catalog.v1 messages
// onto use case commands and back, and nothing else.
//
// Two absences are deliberate. It validates nothing, because the constraints are
// declared in the proto and enforced by an interceptor; and it constructs no
// errors, because every failure arrives already classified and leaves through
// errorx.ToGRPC.
package grpc

import (
	catalogv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/catalog/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/port/in"
)

// CatalogHandler serves CatalogService.
//
// Embedding the generated Unimplemented struct lets a new RPC be added to the
// proto without breaking the build here: the method answers Unimplemented until
// someone writes it.
type CatalogHandler struct {
	catalogv1.UnimplementedCatalogServiceServer

	products in.ProductUseCase
}

var _ catalogv1.CatalogServiceServer = (*CatalogHandler)(nil)

// NewCatalogHandler builds the handler over the driving port.
func NewCatalogHandler(products in.ProductUseCase) *CatalogHandler {
	return &CatalogHandler{products: products}
}
