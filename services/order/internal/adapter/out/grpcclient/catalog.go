package grpcclient

import (
	"context"
	"math"

	catalogv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/catalog/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out"
)

// CatalogGateway calls the catalog service.
type CatalogGateway struct {
	client catalogv1.CatalogServiceClient
}

var _ out.CatalogGateway = (*CatalogGateway)(nil)

// NewCatalogGateway builds the gateway over a generated client.
func NewCatalogGateway(client catalogv1.CatalogServiceClient) *CatalogGateway {
	return &CatalogGateway{client: client}
}

// PriceSKUs returns what each SKU costs now.
//
// The catalog is asked about products and answers with variants, and it is the
// variant that carries a SKU and a price — so this walks them and keeps the
// ones that were asked for. A SKU that comes back priced in a currency this
// service cannot read is dropped rather than guessed at, and the use case
// refuses the line for the same reason it refuses one nobody priced at all.
func (g *CatalogGateway) PriceSKUs(ctx context.Context, skus []domain.SKU) ([]out.PricedSKU, error) {
	wanted := make(map[string]struct{}, len(skus))
	for _, sku := range skus {
		wanted[sku.String()] = struct{}{}
	}

	res, err := g.client.GetVariantsBySKUs(ctx, &catalogv1.GetVariantsBySKUsRequest{
		Skus: keys(wanted),
	})
	if err != nil {
		return nil, err
	}

	priced := make([]out.PricedSKU, 0, len(skus))
	for _, variant := range res.GetVariants() {
		if _, ok := wanted[variant.GetSku()]; !ok {
			continue
		}

		sku, err := domain.NewSKU(variant.GetSku())
		if err != nil {
			continue
		}

		currency, err := domain.NewCurrencyCode(variant.GetPrice().GetCurrencyCode())
		if err != nil {
			continue
		}

		price, err := domain.NewMoney(variant.GetPrice().GetAmountMinor(), currency)
		if err != nil {
			continue
		}

		priced = append(priced, out.PricedSKU{SKU: sku, Price: price})
	}

	return priced, nil
}

func keys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}

	return out
}

// narrow converts a count the domain keeps as an int to the int32 the contract
// declares. The values that reach it are bounded far below this by the proto
// and the domain; the clamp is here so the conversion cannot silently wrap.
func narrow(value int) int32 {
	switch {
	case value < 0:
		return 0
	case value > math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(value)
	}
}
