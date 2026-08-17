package rest

import (
	"net/http"

	catalogv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/catalog/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
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
