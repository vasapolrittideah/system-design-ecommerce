package rest

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	orderv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/order/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
)

// checkout places an order for the signed-in customer.
//
// The user is never in the request: the order service takes it from the
// identity this BFF verified and forwarded, which is what makes this endpoint
// incapable of buying on somebody else's account.
//
// It answers 201 with the order in a pending state. That is the whole of the
// synchronous half — the stock is held and the money is not in yet — and a
// client polls or is told later rather than being held open for a payment
// provider.
func (h *Handler) checkout(w http.ResponseWriter, r *http.Request) {
	var body checkoutRequest
	if err := h.validator.Bind(r, &body); err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	lines := make([]*orderv1.CheckoutLine, 0, len(body.Lines))
	for _, line := range body.Lines {
		lines = append(lines, &orderv1.CheckoutLine{Sku: line.SKU, Quantity: line.Quantity})
	}

	res, err := h.orders.Checkout(r.Context(), &orderv1.CheckoutRequest{
		IdempotencyKey: body.IdempotencyKey,
		Lines:          lines,
	})
	if err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	httpx.WriteJSON(w, r, http.StatusCreated, orderResponse{Order: toOrder(res.GetOrder())})
}

// listOrders is the order history screen.
func (h *Handler) listOrders(w http.ResponseWriter, r *http.Request) {
	var query listOrdersQuery
	if err := h.validator.BindQuery(r, &query); err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	res, err := h.orders.ListOrders(r.Context(), &orderv1.ListOrdersRequest{
		PageSize:  query.PageSize,
		PageToken: query.PageToken,
	})
	if err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	orders := make([]order, 0, len(res.GetOrders()))
	for _, o := range res.GetOrders() {
		orders = append(orders, toOrder(o))
	}

	httpx.WriteJSON(w, r, http.StatusOK, ordersResponse{
		Orders:        orders,
		NextPageToken: res.GetNextPageToken(),
	})
}

// getOrder is the order detail screen.
//
// An order that belongs to somebody else answers 404, because that is what the
// order service answers: confirming that an id exists but is not yours is a
// fact about another customer.
func (h *Handler) getOrder(w http.ResponseWriter, r *http.Request) {
	params := orderPath{ID: chi.URLParam(r, "id")}
	if err := h.validator.Struct(r.Context(), &params); err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	res, err := h.orders.GetOrder(r.Context(), &orderv1.GetOrderRequest{Id: params.ID})
	if err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	httpx.WriteJSON(w, r, http.StatusOK, orderResponse{Order: toOrder(res.GetOrder())})
}
