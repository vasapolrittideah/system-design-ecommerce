package auth

import (
	"crypto/ecdsa"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
)

// SignerConfig is the environment-driven signing configuration. Only the
// identity service loads it, conventionally under "IDENTITY_JWT_".
type SignerConfig struct {
	// PrivateKey is the ES256 signing key, PEM or base64-encoded PEM. It is the
	// credential that mints any user's identity, so it exists in exactly one
	// deployment and never in a verifier's environment.
	//
	// config.Secret rather than string: this is the one value in the repo that
	// forges any user without touching a database, and a logged copy of it is
	// indistinguishable from the real thing.
	PrivateKey config.Secret `env:"PRIVATE_KEY,required"`

	// KeyID names the key, and is stamped on every token as the "kid" header.
	//
	// It buys nothing with one key in play, and cannot be added later: rotating
	// means running two keys while the old tokens drain, and a verifier can only
	// pick between them if the token says which one signed it. Retrofitting that
	// would invalidate every token already in a browser.
	KeyID string `env:"KEY_ID,required"`

	// Issuer is the "iss" claim — who signed this. It should name the
	// deployment, not just the service, so a staging token is not silently
	// valid in production.
	Issuer string `env:"ISSUER,required"`

	// Audience is the "aud" claim — who this token is for.
	Audience string `env:"AUDIENCE,required"`

	// TTL is how long an access token stays valid.
	//
	// Short by design, because an access token cannot be revoked: until it
	// expires, a stolen one works, a deleted user is still logged in, and a
	// role that was taken away is still held. This is the window on all three,
	// and the refresh token is what keeps that window from being felt.
	TTL time.Duration `env:"TTL" envDefault:"15m"`
}

// Signer mints access tokens. It holds the private key and is safe for
// concurrent use.
type Signer struct {
	key *ecdsa.PrivateKey
	cfg SignerConfig
}

// Token is a signed access token together with the moment it stops being valid,
// so a login response can report expires_in without recomputing the TTL policy
// its caller does not own.
type Token struct {
	Value     string
	ExpiresAt time.Time
}

// NewSigner parses the private key and returns a signer for it.
func NewSigner(cfg SignerConfig) (*Signer, error) {
	key, err := parsePrivateKey(cfg.PrivateKey.Reveal())
	if err != nil {
		return nil, err
	}

	if cfg.TTL <= 0 {
		return nil, fmt.Errorf("auth: signer TTL must be positive, got %s", cfg.TTL)
	}

	return &Signer{key: key, cfg: cfg}, nil
}

// MustNewSigner is NewSigner for main and bootstrap wiring, where a service that
// cannot sign has nothing to serve.
func MustNewSigner(cfg SignerConfig) *Signer {
	signer, err := NewSigner(cfg)
	if err != nil {
		panic(err)
	}

	return signer
}

// Sign issues an access token for subject carrying roles.
//
// The claims are assembled here rather than taken from the caller, so every
// token agrees on issuer, audience, and lifetime. Identity decides who gets a
// token and which roles go on it; the shape is not a per-call decision.
func (s *Signer) Sign(subject string, roles []string) (Token, error) {
	if subject == "" {
		return Token{}, fmt.Errorf("auth: sign requires a subject")
	}

	now := time.Now()
	expiresAt := now.Add(s.cfg.TTL)

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    s.cfg.Issuer,
			Audience:  jwt.ClaimStrings{s.cfg.Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
		Roles: roles,
	}

	token := jwt.NewWithClaims(signingMethod, claims)
	token.Header["kid"] = s.cfg.KeyID

	signed, err := token.SignedString(s.key)
	if err != nil {
		return Token{}, fmt.Errorf("auth: sign token: %w", err)
	}

	return Token{Value: signed, ExpiresAt: expiresAt}, nil
}
