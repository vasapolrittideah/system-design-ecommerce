package httpx

import (
	"github.com/go-chi/chi/v5"
)

// NewRouter builds the chi router the Composition API mounts its endpoints on,
// with the middleware every request needs already attached and both of chi's
// fallback handlers replaced.
//
// The chain, outermost first:
//
//  1. [CorrelationID] — so everything after it, a panic included, is logged
//     under the ID the caller was handed back.
//  2. [Recover] — so a panic in a handler becomes a 500 in this API's error
//     shape rather than chi's plain-text stack dump.
//  3. [Validator.Localize] — so validation failures come back in the language
//     the caller asked for.
//
// This is the reverse of pkg/grpcx/server's order, where recovery is outermost.
// There the ID is minted by the logging interceptor, which cannot run before
// recovery without losing panics thrown above it; here the ID is a header the
// caller already sent, so reading it first buys a correlation ID on the panic
// line for nothing.
//
// Everything else is left to the caller: routes, timeouts, and CORS belong to
// the service mounting this, and Kong already handles the edge concerns.
func NewRouter(v *Validator) *chi.Mux {
	r := chi.NewRouter()

	r.Use(CorrelationID)
	r.Use(Recover)
	r.Use(v.Localize)

	r.NotFound(NotFound)
	r.MethodNotAllowed(MethodNotAllowed)

	return r
}
