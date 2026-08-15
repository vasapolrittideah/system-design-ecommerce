// Package auth is where a request stops being anonymous.
//
// Everything downstream assumes the question has been answered: pkg/grpcx
// carries an [grpcx.Identity] on the context, its client forwards it, its server
// reads it back, and none of them verify anything. Here is the one
// implementation of "is this token real" in the repo.
//
// # Who holds which key
//
// Tokens are ES256, so the key that signs and the key that verifies are
// different keys:
//
//	identity service   private key   signs access tokens
//	Kong               public key    verifies signature and expiry at the edge
//	Composition API    public key    verifies again, because the edge is not trusted
//
// That asymmetry is what makes zero trust affordable: a shared secret would put
// the credential that forges any user's identity inside Kong, the process most
// exposed to the outside. ES256 over RS256 for size, on a header that rides
// every request.
//
// # Access tokens only
//
// A refresh token deliberately does not live here: it has to be revocable, which
// a self-contained signed token cannot be. Those are opaque random strings whose
// hash the identity service stores in a table, where a row can be deleted.
//
// # Reason codes
//
// Every rejection is an errorx.KindUnauthenticated carrying one of three reason
// codes, which are API and therefore not renamable:
//
//	TOKEN_MISSING   no bearer token on the request
//	TOKEN_EXPIRED   well-formed and correctly signed, past its exp — refresh
//	TOKEN_INVALID   everything else: bad signature, wrong issuer, malformed
//
// TOKEN_EXPIRED is the only one a client acts on differently. The rest collapse
// on purpose, so someone probing signatures learns nothing from the answer.
package auth

import (
	"github.com/golang-jwt/jwt/v5"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
)

// signingMethod is the one signature this package produces and the one the
// verifier accepts, named once so the two cannot drift into a state where every
// freshly signed token is rejected on arrival.
//
// The verifier pins it rather than reading the token's alg header: a parser that
// believes the token about how to check the token accepts alg "none", and
// accepts the public key replayed as an HMAC secret.
var signingMethod jwt.SigningMethod = jwt.SigningMethodES256

// Claims is the payload of an access token — the shape the identity service
// signs and every verifier expects to find.
//
// It stays small on purpose: a claim rides every request the holder makes and
// cannot be withdrawn before the token expires, so anything a service can look
// up for itself stays out. Roles are here only because authorization checks
// would otherwise call identity on every request.
type Claims struct {
	jwt.RegisteredClaims

	// Roles are the caller's roles as identity saw them at sign time. They say
	// what the user is, never what they may do with a particular aggregate.
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
