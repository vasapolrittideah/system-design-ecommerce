package rest

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	paymentv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/payment/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
)

// providerSignatureHeader is where a provider's signature over the webhook body
// travels.
//
// The provider chooses this name, so it is a literal on both sides rather than
// one constant shared between them: the payment service's copy sits under
// services/payment/internal and is out of reach by construction. Reading the
// wrong header fails every webhook as an invalid signature, with nothing
// pointing at the header as the cause, which is why each side's tests spell the
// name out instead of taking it from the code under test.
const providerSignatureHeader = "X-Provider-Signature"

// startPayment begins one attempt to collect for an order of the caller's.
//
// The amount is not in the request and cannot be: what an order costs is read
// from the order service, so a client that could name its own amount would be
// naming what it pays.
//
// It answers 201 with the attempt as it stands — which may already be settled,
// and for a card needing a 3-D Secure page will be `pending` with somewhere to
// send the customer. Waiting here for the provider is what the whole flow is
// arranged to avoid.
//
// A declined card is a 201 carrying a `failed` attempt rather than an error:
// the order is still open and the customer may try another card, and there is
// nothing about the request that failed.
func (h *Handler) startPayment(w http.ResponseWriter, r *http.Request) {
	params := orderPath{ID: chi.URLParam(r, "id")}
	if err := h.validator.Struct(r.Context(), &params); err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	var body startPaymentRequest
	if err := h.validator.Bind(r, &body); err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	res, err := h.payments.InitiatePayment(r.Context(), &paymentv1.InitiatePaymentRequest{
		OrderId:        params.ID,
		IdempotencyKey: body.IdempotencyKey,
		Method:         body.Method,
	})
	if err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	httpx.WriteJSON(w, r, http.StatusCreated, paymentResponse{
		Payment:       toPayment(res.GetPayment()),
		NextActionURL: res.GetNextActionUrl(),
	})
}

// handleProviderWebhook forwards a provider's callback to the service that can
// read it.
//
// It interprets nothing. Verifying the signature needs the provider's secret
// and knowing what the body means is payment's business, so this hop passes the
// bytes and the signature on and answers with what payment made of them.
//
// The body travels byte for byte, which is the whole reason it is read rather
// than bound: the signature is computed over exactly those bytes, and a hop that
// decoded the JSON and re-encoded it would forward a body that no longer
// verifies — a different key order is enough.
//
// It sits outside the authenticated group because the caller is not a person.
// What stands in for identity is the signature, which proves who wrote the bytes
// rather than who relayed them.
func (h *Handler) handleProviderWebhook(w http.ResponseWriter, r *http.Request) {
	headers := providerCallbackHeaders{Signature: r.Header.Get(providerSignatureHeader)}
	if err := h.validator.Struct(r.Context(), &headers); err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	payload, err := httpx.ReadBody(r)
	if err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	if _, err := h.payments.HandleProviderCallback(r.Context(), &paymentv1.HandleProviderCallbackRequest{
		Payload:   payload,
		Signature: headers.Signature,
	}); err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	// No body, and deliberately not the attempt payment answered with: a
	// provider is not a client of this API and has no use for one, and a
	// callback about a charge made elsewhere settles nothing to describe. What
	// it needs is the 2xx that stops it retrying.
	httpx.WriteJSON(w, r, http.StatusNoContent, nil)
}
