package out

import (
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
)

// AccessToken is a signed access token and the moment it stops being accepted.
//
// The expiry travels beside the value rather than being read back out of it: a
// use case that had to decode its own token to learn when it expires would be
// parsing a credential to recover something it just decided.
type AccessToken struct {
	Value     domain.TokenValue
	ExpiresAt time.Time
}

// AccessTokenIssuer mints the signed half of a token pair.
//
// A port because the signing key, the algorithm, and the claim shape are all
// outside this service's rules — it decides *who* gets a token and which roles
// go on it, and nothing about what a token looks like.
//
// It cannot revoke one, and there is deliberately no method here that pretends
// otherwise: a self-contained signed token is valid until it expires, which is
// the whole reason a refresh token exists to be revoked instead.
type AccessTokenIssuer interface {
	// Issue signs a token for the user, carrying the roles as identity sees them
	// now. The TTL is the issuer's policy, not a parameter, so every token this
	// service mints agrees on how long it lives.
	Issue(userID domain.UserID, roles []domain.Role) (AccessToken, error)
}
