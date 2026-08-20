package rest

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	catalogv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/catalog/v1"
	inventoryv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/inventory/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

// listProducts serves the storefront's product listing screen: one page of the
// catalog, shaped for a grid of cards.
//
// The status filter is not exposed and is not passed on. Catalog reads an unset
// status as ACTIVE only, so this endpoint cannot be asked for drafts by anyone
// who guesses a query parameter — which is the difference between a listing an
// audience is allowed to see and one that trusts the client to filter itself. An
// admin console needs the other statuses and gets them from bff-admin.
func (h *Handler) listProducts(w http.ResponseWriter, r *http.Request) {
	var query listProductsQuery
	if err := h.validator.BindQuery(r, &query); err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	// Catalog is critical to this screen: it is the only thing that can answer
	// what is on sale, so its failure is the request's failure and comes back as
	// the status and reason code it reported, not as a 500 and not as an empty
	// grid pretending the shop is bare.
	//
	// Nothing here passes a deadline along, and nothing should: the request
	// context already carries the BFF's share of the budget from httpx's Timeout
	// middleware, and pkg/grpcx/client uses what it inherits. The 300ms on the
	// client config is the backstop for a call that arrives without one.
	res, err := h.catalog.ListProducts(r.Context(), &catalogv1.ListProductsRequest{
		Category:  query.Category,
		PageSize:  query.PageSize,
		PageToken: query.PageToken,
	})
	if err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	httpx.WriteJSON(w, r, http.StatusOK, productsResponse{
		Products:      toProductCards(res.GetProducts()),
		NextPageToken: res.GetNextPageToken(),
	})
}

// getProduct serves the storefront's product page: one product with its
// variants, and how many of each the warehouse can still sell.
//
// Two calls in sequence rather than a fan-out, because the second's input is the
// first's output — inventory is keyed by SKU, and the SKUs are what catalog
// answers with. There is nothing here to run in parallel, and an errgroup would
// only make that harder to read.
//
// Catalog is critical and inventory is optional, which is the whole difference
// between them: a page missing its description is not a product page, while one
// whose availability reads "unknown" still says what this thing is and what it
// costs. So catalog's failure is the request's, and inventory's is a null on
// every variant.
//
// The status filter is not exposed and is not passed on, exactly as on the
// listing: catalog reads an unset status as ACTIVE only, so a draft is a 404
// here rather than a page nobody published.
func (h *Handler) getProduct(w http.ResponseWriter, r *http.Request) {
	params := productPath{ID: chi.URLParam(r, "id")}
	if err := h.validator.Struct(r.Context(), &params); err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	res, err := h.catalog.GetProduct(r.Context(), &catalogv1.GetProductRequest{Id: params.ID})
	if err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	product := res.GetProduct()

	httpx.WriteJSON(w, r, http.StatusOK, productResponse{
		Product: toProductDetail(product, h.availableBySKU(r, product.GetVariants())),
	})
}

// availableBySKU reads how much of each variant is left, and returns nil when
// the warehouse did not answer.
//
// Every error degrades, including one that says this BFF asked wrongly. A
// dependency that failed the page on a bad request would be optional only while
// it worked; what a malformed call costs instead is the warn line below and a
// rejected request in inventory's own metrics.
func (h *Handler) availableBySKU(r *http.Request, variants []*catalogv1.Variant) availableBySKU {
	if len(variants) == 0 {
		// GetStockBySKUs refuses an empty list, so asking would spend a round
		// trip to be told what is already known here. Empty and not nil: there
		// is nothing to be uncertain about.
		return availableBySKU{}
	}

	skus := make([]string, 0, len(variants))
	for _, variant := range variants {
		skus = append(skus, variant.GetSku())
	}

	res, err := h.inventory.GetStockBySKUs(r.Context(), &inventoryv1.GetStockBySKUsRequest{Skus: skus})
	if err != nil {
		logger.From(r.Context()).Warn("inventory did not answer; serving the page without availability",
			zap.Error(err),
			zap.Int("skus", len(skus)),
		)

		return nil
	}

	available := make(availableBySKU, len(res.GetItems()))
	for _, item := range res.GetItems() {
		available[item.GetSku()] = item.GetAvailable()
	}

	return available
}
