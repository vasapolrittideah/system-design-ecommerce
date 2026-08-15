package auth

import (
	"crypto/ecdsa"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
)

// Reason codes attached to a rejection. They are what a client branches on, so
// they are part of the API — see the package doc for what each means to the
// caller receiving it.
const (
	reasonTokenMissing = "TOKEN_MISSING"
	reasonTokenExpired = "TOKEN_EXPIRED"
	reasonTokenInvalid = "TOKEN_INVALID"
)

// VerifierConfig is the environment-driven verification configuration, loaded by
// whichever process is a caller's first reachable hop — today the Composition
// API, conventionally under "COMPOSITION_JWT_".
type VerifierConfig struct {
	// PublicKey is the ES256 verification key, PEM or base64-encoded PEM. It
	// cannot sign, which is why it is safe to hand to Kong and to every process
	// that has to check a token.
	PublicKey string `env:"PUBLIC_KEY,required"`

	// Issuer is the "iss" a token must carry. It is checked, not merely read:
	// without it a token minted by any other deployment sharing the key pair —
	// staging, a developer's laptop — is accepted here.
	Issuer string `env:"ISSUER,required"`

	// Audience is the "aud" a token must carry.
	Audience string `env:"AUDIENCE,required"`

	// Leeway is how much clock disagreement to tolerate on exp, nbf, and iat.
	//
	// Signer and verifier are different pods on different nodes, and their
	// clocks drift. Without leeway a token is briefly not-yet-valid at the
	// moment it is issued, which shows up as a rare login that fails once and
	// works on retry — the kind of bug that gets closed as unreproducible. The
	// cost is that a token lives this much longer than its TTL says.
	Leeway time.Duration `env:"LEEWAY" envDefault:"30s"`
}

// Verifier checks access tokens against a public key. It is safe for concurrent
// use.
type Verifier struct {
	key    *ecdsa.PublicKey
	parser *jwt.Parser
}

// NewVerifier parses the public key and builds the parser that will enforce
// every claim in cfg.
func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	key, err := parsePublicKey(cfg.PublicKey)
	if err != nil {
		return nil, err
	}

	parser := jwt.NewParser(
		// Pinned rather than read off the token. See [signingMethod].
		jwt.WithValidMethods([]string{signingMethod.Alg()}),
		jwt.WithIssuer(cfg.Issuer),
		jwt.WithAudience(cfg.Audience),
		// A token with no exp never expires, and this package has no revocation
		// list to catch one that escapes. Refusing to accept it is the only
		// defence there is.
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(cfg.Leeway),
	)

	return &Verifier{key: key, parser: parser}, nil
}

// MustNewVerifier is NewVerifier for main and bootstrap wiring.
func MustNewVerifier(cfg VerifierConfig) *Verifier {
	verifier, err := NewVerifier(cfg)
	if err != nil {
		panic(err)
	}

	return verifier
}

// Verify checks a token's signature and claims and returns what it says.
//
// Every failure comes back as [errorx.KindUnauthenticated] carrying one of the
// three reason codes in the package doc. The underlying parser error is wrapped
// rather than discarded, so the log keeps the detail the client is not told.
func (v *Verifier) Verify(token string) (Claims, error) {
	if token == "" {
		return Claims{}, errorx.New(errorx.KindUnauthenticated, "no bearer token").
			WithReason(reasonTokenMissing)
	}

	claims := &Claims{}
	if err := v.parse(token, claims); err != nil {
		return Claims{}, err
	}

	// A token that verifies but names nobody would become an identity with an
	// empty user ID, which every reader downstream treats as "no user behind
	// this call" — an anonymous request wearing a valid signature.
	if claims.Subject == "" {
		return Claims{}, errorx.New(errorx.KindUnauthenticated, "token has no subject").
			WithReason(reasonTokenInvalid)
	}

	return *claims, nil
}

// parse runs the token through the parser and reduces whatever comes back to
// TOKEN_EXPIRED or TOKEN_INVALID.
//
// The message is fixed per branch rather than assembled from the token, so
// nothing an attacker controls is reflected into the response body or the log.
func (v *Verifier) parse(token string, claims *Claims) error {
	_, err := v.parser.ParseWithClaims(token, claims, v.keyFunc)
	if err == nil {
		return nil
	}

	// Expiry is the one failure a client can act on, and the only one it is
	// told apart from the rest: it means the refresh token is due, where
	// everything else means log in again.
	if errors.Is(err, jwt.ErrTokenExpired) {
		return errorx.Wrap(err, errorx.KindUnauthenticated, "token expired").
			WithReason(reasonTokenExpired)
	}

	return errorx.Wrap(err, errorx.KindUnauthenticated, "token is not valid").
		WithReason(reasonTokenInvalid)
}

// keyFunc supplies the verification key.
//
// It ignores the kid header for now: there is one key, and rejecting a token
// whose kid does not match it would add a way to fail without adding a way to be
// safe. When a second key exists this becomes a lookup, and the tokens already
// signed still resolve, because the signer has been stamping kid from the start.
func (v *Verifier) keyFunc(*jwt.Token) (any, error) {
	return v.key, nil
}
