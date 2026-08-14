// Package auth is where a request stops being anonymous.
//
// Everything downstream of this package assumes the question has already been
// answered: pkg/grpcx carries an [grpcx.Identity] on the context, pkg/grpcx/client
// forwards it to the next hop, and pkg/grpcx/server reads it back out. None of
// them verify anything. This package is the one place a signed token is turned
// into that identity, so there is exactly one implementation of "is this token
// real" in the repo rather than one per entry point.
//
// # Who holds which key
//
// Tokens are ES256 — an elliptic-curve signature, so the key that signs and the
// key that verifies are different keys:
//
//	identity service   private key   signs access tokens
//	Kong               public key    verifies signature and expiry at the edge
//	Composition API    public key    verifies again, because the edge is not trusted
//
// This is what makes the zero-trust rule affordable. With a shared secret every
// verifier would also be able to mint tokens, and Kong — the process most
// exposed to the outside — would be holding the credential that forges any
// user's identity. Here it holds a key that can only ever say no.
//
// ES256 over RS256 for size: a P-256 key is 32 bytes against 256, and the
// signature it produces is 64 bytes against 256, on a header that rides every
// single request. Kong's jwt plugin verifies both.
//
// # Access tokens only
//
// What this package signs is the short-lived access token, and nothing else. A
// refresh token deliberately does not live here: it has to be revocable, which
// a self-contained signed token cannot be — logging out would have to wait for
// the token to expire on its own. Refresh tokens are opaque random strings whose
// hash the identity service stores in its own table, where a row can be deleted.
//
// # Reason codes
//
// Every rejection is an errorx.KindUnauthenticated — a 401, never the 403 that
// errorx.KindUnauthorized would produce, because the two tell a frontend to do
// opposite things. It carries one of three reason codes, which are API and
// therefore not renamable:
//
//	TOKEN_MISSING   no bearer token on the request
//	TOKEN_EXPIRED   well-formed and correctly signed, past its exp — refresh
//	TOKEN_INVALID   everything else: bad signature, wrong issuer, malformed
//
// Only the first two are worth telling a client apart, and TOKEN_EXPIRED is the
// one it acts on: it means "use your refresh token", where TOKEN_INVALID means
// "log in again". The rest collapse into TOKEN_INVALID on purpose — an attacker
// probing signatures learns nothing from the answer, and no client can do
// anything different about a wrong audience than about a wrong signature.
package auth

import (
	"github.com/golang-jwt/jwt/v5"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
)

// algorithm is the only signature algorithm this package will produce or accept.
//
// The verifier pins it explicitly rather than trusting the alg header, which is
// the oldest bug in JWT: a parser that believes the token about how to check the
// token accepts alg "none", and accepts an RS256 public key replayed as an HS256
// shared secret. Pinning here means a token signed any other way is rejected
// before its signature is ever looked at.
const algorithm = "ES256"

// Claims is the payload of an access token — the shape the identity service
// signs and every verifier expects to find.
//
// It stays small on purpose. A claim here is copied onto every request the
// holder makes for the lifetime of the token and cannot be withdrawn before it
// expires, so anything that changes more often than the token does, or that a
// service can look up for itself, does not belong in it. Email, display name,
// and the user's cart are all lookups; roles are here because authorization
// checks would otherwise need a call to identity on every single request.
type Claims struct {
	jwt.RegisteredClaims

	// Roles are the caller's roles as identity saw them at sign time. They say
	// what the user is, never what they may do with a particular aggregate —
	// that rule belongs to the service owning it.
	Roles []string `json:"roles,omitempty"`
}

// Identity is the claims reduced to what the rest of the repo passes around.
//
// The subject is the user ID: it is the registered claim for exactly this, and
// putting the ID in a custom claim beside it would leave two fields that have to
// agree.
func (c Claims) Identity() grpcx.Identity {
	return grpcx.Identity{UserID: c.Subject, Roles: c.Roles}
}
