// Package rest is the driving adapter for the web BFF: it decodes a request
// body, calls the services that hold the answer, and shapes the result for one
// screen.
//
// It holds no business rules. Where a handler here looks like it is deciding
// something, it is mapping — a status, a shape, or a name — and anything that
// resembles a policy belongs in the service that owns the data.
package rest

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
)

// Handler serves the web client's endpoints.
//
// It depends on the generated client interface rather than a port of its own.
// The proto already is the contract, and a hand-written interface mirroring it
// method for method would be a second copy to keep in step — a port earns its
// place when a use case has to name something the generated code does not, which
// is the day composition logic arrives.
type Handler struct {
	identity  identityv1.IdentityServiceClient
	validator *httpx.Validator
}

// NewHandler builds the handler over the clients it fans out to.
func NewHandler(identity identityv1.IdentityServiceClient, validator *httpx.Validator) *Handler {
	return &Handler{identity: identity, validator: validator}
}

// Mount attaches every route to r, taking the authentication middleware rather
// than building it, so which routes require a user is decided here — next to the
// route table, where it can be read in one pass.
//
// The /auth group is deliberately outside it. Registering and signing in cannot
// present a token yet, and refresh must work precisely when the access token has
// expired: requiring one there would make a session unrenewable at the only
// moment renewal is needed.
func (h *Handler) Mount(r chi.Router, authenticate func(http.Handler) http.Handler) {
	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			r.Post("/register", h.register)
			r.Post("/login", h.login)
			r.Post("/refresh", h.refresh)
			r.Post("/logout", h.logout)
		})

		r.Group(func(r chi.Router) {
			r.Use(authenticate)

			r.Get("/me", h.me)
		})
	})
}
