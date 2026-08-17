package rest

import (
	"time"

	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
)

// The bodies the /auth endpoints accept.
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

// What they answer with. Signing in returns the person as well as the
// credentials, so a client renders a header without a second call; refreshing
// returns only the pair, because nothing about the account has changed.
type (
	sessionResponse struct {
		Tokens tokenPair `json:"tokens"`
		User   user      `json:"user"`
	}

	tokensResponse struct {
		Tokens tokenPair `json:"tokens"`
	}

	tokenPair struct {
		AccessToken           string    `json:"accessToken"`
		RefreshToken          string    `json:"refreshToken"`
		AccessTokenExpiresAt  time.Time `json:"accessTokenExpiresAt"`
		RefreshTokenExpiresAt time.Time `json:"refreshTokenExpiresAt"`
	}
)

func toTokenPair(p *identityv1.TokenPair) tokenPair {
	return tokenPair{
		AccessToken:           p.GetAccessToken(),
		RefreshToken:          p.GetRefreshToken(),
		AccessTokenExpiresAt:  asTime(p.GetAccessTokenExpiresAt()),
		RefreshTokenExpiresAt: asTime(p.GetRefreshTokenExpiresAt()),
	}
}
