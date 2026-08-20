package rest

import (
	catalogv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/catalog/v1"
	commonv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/common/v1"
)

// listProductsQuery is the listing's query string, bound by its `query` tags and
// checked by the same `validate` tags a body goes through, so the endpoint has
// no hand-written parsing in it either.
//
// A parameter this struct does not name is ignored rather than rejected: a link
// into the storefront arrives carrying whatever the campaign that produced it
// attached, and none of that is this API's business.
type listProductsQuery struct {
	// Exact match on a category slug, and every category when empty.
	//
	// Only the length is checked here. What a slug may look like is catalog's
	// rule, and a second copy of its pattern would be one more thing to keep in
	// step for the sake of a round trip that already answers with the field
	// named.
	Category string `query:"category" validate:"omitempty,max=64"`

	// 0 leaves the page size to catalog, which is where the default belongs —
	// the shape of a page is a property of the data, not of this screen.
	PageSize int32 `query:"pageSize" validate:"gte=0,lte=100"`

	// nextPageToken from the previous response, opaque to this tier as much as
	// to the client: it is catalog's cursor, and the BFF passes it back
	// unread.
	PageToken string `query:"pageToken" validate:"omitempty,max=512"`
}

// What the listing screen answers with.
type (
	productsResponse struct {
		Products []productCard `json:"products"`

		// Absent on the last page, and passed back as pageToken to ask for the
		// next one.
		NextPageToken string `json:"nextPageToken,omitempty"`
	}

	// productCard is one product as a grid of cards needs it, which is much less
	// than catalog holds about it.
	//
	// The description and the variant list are deliberately not here. A listing
	// of twenty products would carry twenty descriptions of up to four thousand
	// characters and every SKU in the catalog to render text no card shows —
	// the product page asks for those, and asks for one product.
	productCard struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Category string `json:"category"`

		// The cheapest variant's price, which is what "from ฿x" on a card is
		// quoting. Null for a product with no variants — a state the storefront
		// listing should never see, since publishing requires one, and a price
		// of zero would be the wrong way to say so if it ever did.
		PriceFrom *money `json:"priceFrom"`

		// How many ways this product can be bought, so a card knows whether
		// PriceFrom is the price or the lowest of several.
		VariantCount int `json:"variantCount"`
	}

	// money is an amount in a currency's minor unit — 1050 is THB 10.50 — with
	// the currency saying how many decimal places that is. Formatting is the
	// client's, because the locale the amount is rendered in is the browser's
	// and not this API's.
	money struct {
		AmountMinor  int64  `json:"amountMinor"`
		CurrencyCode string `json:"currencyCode"`
	}
)

// toProductCards maps a page of products onto the cards a listing renders.
//
// An empty page comes back as an empty array rather than null, for the same
// reason a user's roles do: a client should not need a nil check to iterate a
// collection that is simply empty.
func toProductCards(products []*catalogv1.Product) []productCard {
	cards := make([]productCard, 0, len(products))
	for _, product := range products {
		cards = append(cards, toProductCard(product))
	}

	return cards
}

func toProductCard(p *catalogv1.Product) productCard {
	return productCard{
		ID:           p.GetId(),
		Name:         p.GetName(),
		Category:     p.GetCategory(),
		PriceFrom:    lowestPrice(p.GetVariants()),
		VariantCount: len(p.GetVariants()),
	}
}

// lowestPrice picks the variant a card quotes, and returns nil when there is
// none to quote.
//
// Comparing the amounts alone is safe because catalog holds every variant of one
// product to a single currency; comparing across two currencies would be a bug
// this function could not detect.
func lowestPrice(variants []*catalogv1.Variant) *money {
	var lowest *commonv1.Money

	for _, variant := range variants {
		price := variant.GetPrice()
		if price == nil {
			continue
		}

		if lowest == nil || price.GetAmountMinor() < lowest.GetAmountMinor() {
			lowest = price
		}
	}

	return toMoney(lowest)
}

// toMoney maps a price, and answers nil for an absent one rather than a zero
// amount: unpriced and free are different facts, and only one of them is a
// price.
func toMoney(m *commonv1.Money) *money {
	if m == nil {
		return nil
	}

	return &money{
		AmountMinor:  m.GetAmountMinor(),
		CurrencyCode: m.GetCurrencyCode(),
	}
}

// productPath is the product page's path parameter, checked through the same
// `validate` tags a body and a query string go through so this handler verifies
// nothing by hand either.
//
// It is checked here rather than left to catalog's protovalidate, which would
// also refuse it: a rejection from downstream arrives as a bare 400 where every
// other bad request in this API carries `error.fields` naming what was wrong.
// The round trip it saves is the smaller half of the reason.
type productPath struct {
	ID string `json:"id" validate:"required,uuid"`
}

// availableBySKU is how many of each SKU may still be sold, and nil when the
// warehouse did not answer.
//
// A SKU inventory does not track is simply absent from a non-nil map, and reads
// back as a real zero: nothing has been stocked, so nothing can be bought. Only
// the nil map means nobody knows.
type availableBySKU map[string]int32

// quantity reports a SKU's availability, and false when it is not known.
func (a availableBySKU) quantity(sku string) (int32, bool) {
	if a == nil {
		return 0, false
	}

	return a[sku], true
}

// What the product page answers with.
type (
	productResponse struct {
		Product productDetail `json:"product"`
	}

	// productDetail is one product as its own page needs it, which is
	// everything a card leaves out: the words, and every way it can be bought.
	//
	// There is no status field. The storefront is only ever answered about
	// published products, so it would carry the same value on every response
	// and tell a client nothing.
	productDetail struct {
		ID          string           `json:"id"`
		Name        string           `json:"name"`
		Description string           `json:"description"`
		Category    string           `json:"category"`
		Variants    []productVariant `json:"variants"`
	}

	// productVariant is one buyable unit: what it costs, what distinguishes it
	// from its siblings, and whether there is one left.
	productVariant struct {
		ID  string `json:"id"`
		SKU string `json:"sku"`

		// Null for a variant the catalog holds no price for — a state a
		// published product should not be in, and one a zero would describe
		// wrongly.
		Price *money `json:"price"`

		// Whatever the catalog was told distinguishes this variant — {"size":
		// "M"}. Opaque here as much as there: no rule in this tier reads a key.
		Attributes map[string]string `json:"attributes"`

		// How many may still be bought, and null when the warehouse did not
		// answer — which is a different thing from zero and the client should
		// render it differently.
		//
		// The count is passed through rather than reduced to a flag. Turning it
		// into "only a few left" is a policy about what a shopper may learn, and
		// a policy belongs to the service that owns the number.
		AvailableQuantity *int32 `json:"availableQuantity"`
	}
)

// toProductDetail maps a product and what the warehouse said about it onto the
// page that renders them.
func toProductDetail(p *catalogv1.Product, available availableBySKU) productDetail {
	variants := make([]productVariant, 0, len(p.GetVariants()))
	for _, v := range p.GetVariants() {
		variants = append(variants, toProductVariant(v, available))
	}

	return productDetail{
		ID:          p.GetId(),
		Name:        p.GetName(),
		Description: p.GetDescription(),
		Category:    p.GetCategory(),
		Variants:    variants,
	}
}

func toProductVariant(v *catalogv1.Variant, available availableBySKU) productVariant {
	attributes := v.GetAttributes()
	if attributes == nil {
		// An empty object rather than null, for the reason a user's roles are
		// an empty array: a client should not need a nil check to read a
		// collection that is simply empty.
		attributes = map[string]string{}
	}

	variant := productVariant{
		ID:         v.GetId(),
		SKU:        v.GetSku(),
		Price:      toMoney(v.GetPrice()),
		Attributes: attributes,
	}

	if quantity, known := available.quantity(v.GetSku()); known {
		variant.AvailableQuantity = &quantity
	}

	return variant
}
