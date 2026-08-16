package rest

import (
	"net/http"

	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
)

// me returns the signed-in user.
//
// The ID comes from the verified token on the context, never from the URL or a
// query parameter, which is what makes this endpoint incapable of reading
// somebody else's account.
func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	identity, ok := grpcx.IdentityFrom(r.Context())
	if !ok || identity.UserID == "" {
		// Unreachable behind the authentication middleware, and Internal rather
		// than Unauthenticated on purpose: reaching here means this route was
		// mounted without it, and answering 401 would send the client off to
		// refresh a token that was never the problem.
		httpx.WriteError(w, r, errorx.New(errorx.KindInternal, "authenticated route reached without an identity"))

		return
	}

	res, err := h.identity.GetUser(r.Context(), &identityv1.GetUserRequest{Id: identity.UserID})
	if err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	httpx.WriteJSON(w, r, http.StatusOK, userResponse{User: toUser(res.GetUser())})
}
