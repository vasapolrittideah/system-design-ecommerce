package rest

import (
	"net/http"

	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
)

// register creates an account and returns the person, no tokens.
//
// Registering and signing in stay separate acts, as they are on the proto: a
// client that only wanted an account is not handed a credential it has to decide
// what to do with.
func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := h.validator.Bind(r, &req); err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	res, err := h.identity.Register(r.Context(), &identityv1.RegisterRequest{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	httpx.WriteJSON(w, r, http.StatusCreated, userResponse{User: toUser(res.GetUser())})
}

// login exchanges credentials for a token pair and the user behind them.
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := h.validator.Bind(r, &req); err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	res, err := h.identity.Login(r.Context(), &identityv1.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	httpx.WriteJSON(w, r, http.StatusOK, sessionResponse{
		Tokens: toTokenPair(res.GetTokens()),
		User:   toUser(res.GetUser()),
	})
}

// refresh exchanges a refresh token for a new pair.
//
// A failure here comes back as 401 with REFRESH_TOKEN_INVALID, which is the
// client's signal to sign in again rather than to retry. Retrying is in fact the
// one thing it must not do: the token this call spends is gone, and presenting
// it a second time is indistinguishable from the theft that rotation exists to
// detect — the identity service answers it by ending the whole chain.
func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshTokenRequest
	if err := h.validator.Bind(r, &req); err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	res, err := h.identity.RefreshToken(r.Context(), &identityv1.RefreshTokenRequest{
		RefreshToken: req.RefreshToken,
	})
	if err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	httpx.WriteJSON(w, r, http.StatusOK, tokensResponse{Tokens: toTokenPair(res.GetTokens())})
}

// logout revokes the presented token's whole rotation chain.
//
// 204 with no body, and it succeeds for a token that was already revoked, never
// issued, or expired: logging out is a state the caller wants to reach rather
// than a change they are making. It does not invalidate the access token already
// in their hands, which keeps working until it expires — a screen that must stop
// responding immediately has to ask the service that owns it.
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	var req logoutRequest
	if err := h.validator.Bind(r, &req); err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	if _, err := h.identity.Logout(r.Context(), &identityv1.LogoutRequest{
		RefreshToken: req.RefreshToken,
	}); err != nil {
		httpx.WriteError(w, r, err)

		return
	}

	httpx.WriteJSON(w, r, http.StatusNoContent, nil)
}
