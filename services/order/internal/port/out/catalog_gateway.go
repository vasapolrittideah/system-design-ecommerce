package out

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
)

// PricedSKU is what one sellable unit costs right now.
type PricedSKU struct {
	SKU   domain.SKU
	Price domain.Money
}

// CatalogGateway is what this service may ask of the catalog.
//
// It asks for prices and nothing else. What a thing costs is the catalog's
// answer; what it cost at the moment of checkout is this service's record, and
// the copy made here is what keeps an order from being rewritten by the next
// price change.
type CatalogGateway interface {
	// PriceSKUs returns the current price of each SKU, in one call. A loop of
	// single lookups is the N+1 this signature exists to prevent.
	//
	// A SKU the catalog does not sell is absent from the result rather than an
	// error: which of them the caller cannot do without is the caller's rule.
	PriceSKUs(ctx context.Context, skus []domain.SKU) ([]PricedSKU, error)
}
