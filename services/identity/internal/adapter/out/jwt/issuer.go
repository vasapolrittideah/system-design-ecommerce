// Package jwt is the driven adapter for signing: it turns a user and their
// roles into a signed access token by handing them to pkg/auth.
//
// It is thin on purpose. What a token looks like — the algorithm, the claim
// names, the issuer and audience, the lifetime — is settled once in pkg/auth so
// that every verifier in the repo agrees with the one signer; nothing about that
// shape is this service's to decide, and the only reason this package exists is
// so the use case can name a port instead of a key.
package jwt

import (
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/auth"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/out"
)

// Issuer implements the access token port over an ES256 signer.
type Issuer struct {
	signer *auth.Signer
}

var _ out.AccessTokenIssuer = (*Issuer)(nil)

// NewIssuer builds the issuer over a signer holding the private key.
func NewIssuer(signer *auth.Signer) *Issuer {
	return &Issuer{signer: signer}
}

// Issue signs an access token for the user.
//
// The subject is the user ID, which is what every other service reads back as
// the caller's identity, and the roles ride along so an authorization check
// elsewhere does not become a call to this service on every request.
func (i *Issuer) Issue(userID domain.UserID, roles []domain.Role) (out.AccessToken, error) {
	names := make([]string, 0, len(roles))
	for _, role := range roles {
		names = append(names, string(role))
	}

	token, err := i.signer.Sign(userID.String(), names)
	if err != nil {
		return out.AccessToken{}, err
	}

	return out.AccessToken{
		Value:     domain.TokenValue(token.Value),
		ExpiresAt: token.ExpiresAt,
	}, nil
}
