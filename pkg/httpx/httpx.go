// Package httpx is the HTTP half of what pkg/errorx and pkg/grpcx do for gRPC:
// the wire format the Composition API answers the frontend in, in one place, so
// every endpoint answers the same shape.
//
// Services never import this package. They speak gRPC, and pkg/errorx already
// carries their errors to the edge; only the BFF turns those into HTTP.
//
// # Wiring
//
//	v := httpx.MustNewValidator()
//	r := httpx.NewRouter(v)
//
//	r.Post("/api/v1/carts/{cartId}/items", func(w http.ResponseWriter, r *http.Request) {
//		var req AddCartItemRequest
//		if err := v.Bind(r, &req); err != nil {
//			httpx.WriteError(w, r, err)
//			return
//		}
//
//		item, err := h.carts.AddItem(r.Context(), toCommand(r, req))
//		if err != nil {
//			httpx.WriteError(w, r, err)   // gRPC status from downstream, mapped
//			return
//		}
//
//		httpx.WriteJSON(w, r, http.StatusCreated, toItemDTO(item))
//	})
//
// # JSON is camelCase
//
// Every field the frontend sees is camelCase, including the ones this package
// emits itself. DTOs carry explicit json tags saying so, and the validator
// reports failures under the same tag rather than the Go field name — a client
// that sent unitPrice is told unitPrice is wrong, not UnitPrice.
//
// # Two kinds of bad request
//
// A body that will not decode is a bug in the client, so it comes back as a
// reason code with no translated prose. A body that decodes but breaks a rule is
// a person's mistake, so every failing field comes back with a message in the
// language they asked for. See [Validator].
package httpx

import (
	"bytes"
	"encoding/json"
	"net/http"

	"go.uber.org/zap"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

// contentTypeJSON is written on every response with a body, and required on
// every request that carries one.
const contentTypeJSON = "application/json"

// WriteJSON writes v as the response body under status.
//
// The body is encoded into memory before anything is written, because the status
// line goes out with the first byte: encoding straight into the ResponseWriter
// and failing halfway would send a 200 followed by a truncated object.
//
// A nil v writes the status and no body.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	if v == nil {
		w.WriteHeader(status)

		return
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		// The handler already decided this request succeeded, so there is
		// nothing left to tell the client that is not a lie. Record what
		// happened and answer the only honest status remaining.
		logger.From(r.Context()).Error("encode response body", zap.Error(err))
		WriteError(w, r, errEncodeResponse)

		return
	}

	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)

	// Nothing can be done about a write failure this late — the status is
	// already on the wire and the client is the one who left.
	_, _ = w.Write(buf.Bytes())
}
