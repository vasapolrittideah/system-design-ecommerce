package rest

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/catalog/v1"
	commonv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/common/v1"
	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
)

// The request bodies this API accepts.
//
// They are hand-written rather than generated from the protos, so a field the
// identity service adds to its contract does not appear on the public API by
// accident, and a client is never bound to a shape that exists for east-west
// traffic. That separation is also why the `validate` tags below restate rules
// the protos already declare: this is the one place in the repo protovalidate
// cannot reach, and an unvalidated body would otherwise cost a network round
// trip to be rejected.
type (
	registerRequest struct {
		Email string `json:"email" validate:"required,email,max=254"`

		// Length only, matching the proto. Composition rules push users toward
		// predictable passwords and belong nowhere near a transport contract.
		Password string `json:"password" validate:"required,min=8,max=256"`
	}

	loginRequest struct {
		Email string `json:"email" validate:"required,email,max=254"`

		// No minimum beyond one character, unlike registration: the rule a
		// caller has to satisfy here is whatever their password already is, and
		// a policy tightened afterwards must not lock them out of an account
		// they can still open.
		Password string `json:"password" validate:"required,max=256"`
	}

	// refreshTokenRequest carries the token in the body, never in a header.
	//
	// The Authorization header rides every request to every service, which is
	// the right place for a 15-minute access token and the wrong place for the
	// long-lived credential that mints new ones. This one has a single
	// destination and travels only there.
	refreshTokenRequest struct {
		RefreshToken string `json:"refreshToken" validate:"required,max=512"`
	}

	logoutRequest struct {
		RefreshToken string `json:"refreshToken" validate:"required,max=512"`
	}
)

// The query strings this API reads. Bound by their `query` tags and checked by
// the same `validate` tags a body goes through, so a listing endpoint has no
// hand-written parsing in it either.
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

// The response bodies this API returns, camelCase throughout.
type (
	userResponse struct {
		User user `json:"user"`
	}

	sessionResponse struct {
		Tokens tokenPair `json:"tokens"`
		User   user      `json:"user"`
	}

	tokensResponse struct {
		Tokens tokenPair `json:"tokens"`
	}

	user struct {
		ID        string    `json:"id"`
		Email     string    `json:"email"`
		Roles     []string  `json:"roles"`
		CreatedAt time.Time `json:"createdAt"`
		UpdatedAt time.Time `json:"updatedAt"`
	}

	tokenPair struct {
		AccessToken           string    `json:"accessToken"`
		RefreshToken          string    `json:"refreshToken"`
		AccessTokenExpiresAt  time.Time `json:"accessTokenExpiresAt"`
		RefreshTokenExpiresAt time.Time `json:"refreshTokenExpiresAt"`
	}

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

// toUser maps the service's view of a person onto the public one.
//
// Roles come back as an empty array rather than null for a user with none, so a
// client can iterate without a nil check — JSON's two ways of saying "nothing"
// are one more branch than any caller needs.
func toUser(u *identityv1.User) user {
	roles := u.GetRoles()
	if roles == nil {
		roles = []string{}
	}

	return user{
		ID:        u.GetId(),
		Email:     u.GetEmail(),
		Roles:     roles,
		CreatedAt: asTime(u.GetCreatedAt()),
		UpdatedAt: asTime(u.GetUpdatedAt()),
	}
}

func toTokenPair(p *identityv1.TokenPair) tokenPair {
	return tokenPair{
		AccessToken:           p.GetAccessToken(),
		RefreshToken:          p.GetRefreshToken(),
		AccessTokenExpiresAt:  asTime(p.GetAccessTokenExpiresAt()),
		RefreshTokenExpiresAt: asTime(p.GetRefreshTokenExpiresAt()),
	}
}

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

	if lowest == nil {
		return nil
	}

	return &money{
		AmountMinor:  lowest.GetAmountMinor(),
		CurrencyCode: lowest.GetCurrencyCode(),
	}
}

// asTime converts a proto timestamp, mapping an absent one to the zero time
// rather than to 1970 — AsTime on a nil timestamp answers the Unix epoch, which
// would reach a client as a real date it could render.
func asTime(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}

	return ts.AsTime()
}
