package rest

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
)

// The request bodies this API accepts.
//
// They are hand-written rather than generated from the protos, so a field the
// identity service adds to its contract does not appear on the public API by
// accident, and a client is never bound to a shape that exists for east-west
// traffic. That separation is also why the `validate` tags below restate rules
// the protos already declare: this is the one place in the repo protovalidate
// cannot reach, and an unvalidated body would otherwise cost a network round
// trip to be rejected.
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

// The response bodies this API returns, camelCase throughout.
type (
	userResponse struct {
		User user `json:"user"`
	}

	sessionResponse struct {
		Tokens tokenPair `json:"tokens"`
		User   user      `json:"user"`
	}

	tokensResponse struct {
		Tokens tokenPair `json:"tokens"`
	}

	user struct {
		ID        string    `json:"id"`
		Email     string    `json:"email"`
		Roles     []string  `json:"roles"`
		CreatedAt time.Time `json:"createdAt"`
		UpdatedAt time.Time `json:"updatedAt"`
	}

	tokenPair struct {
		AccessToken           string    `json:"accessToken"`
		RefreshToken          string    `json:"refreshToken"`
		AccessTokenExpiresAt  time.Time `json:"accessTokenExpiresAt"`
		RefreshTokenExpiresAt time.Time `json:"refreshTokenExpiresAt"`
	}
)

// toUser maps the service's view of a person onto the public one.
//
// Roles come back as an empty array rather than null for a user with none, so a
// client can iterate without a nil check — JSON's two ways of saying "nothing"
// are one more branch than any caller needs.
func toUser(u *identityv1.User) user {
	roles := u.GetRoles()
	if roles == nil {
		roles = []string{}
	}

	return user{
		ID:        u.GetId(),
		Email:     u.GetEmail(),
		Roles:     roles,
		CreatedAt: asTime(u.GetCreatedAt()),
		UpdatedAt: asTime(u.GetUpdatedAt()),
	}
}

func toTokenPair(p *identityv1.TokenPair) tokenPair {
	return tokenPair{
		AccessToken:           p.GetAccessToken(),
		RefreshToken:          p.GetRefreshToken(),
		AccessTokenExpiresAt:  asTime(p.GetAccessTokenExpiresAt()),
		RefreshTokenExpiresAt: asTime(p.GetRefreshTokenExpiresAt()),
	}
}

// asTime converts a proto timestamp, mapping an absent one to the zero time
// rather than to 1970 — AsTime on a nil timestamp answers the Unix epoch, which
// would reach a client as a real date it could render.
func asTime(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}

	return ts.AsTime()
}
