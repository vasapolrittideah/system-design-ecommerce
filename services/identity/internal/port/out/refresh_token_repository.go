package out

import (
	"context"
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
)

// RefreshTokenRepository stores the half of a credential pair that can be taken
// back.
//
// There is no Update: the only change a stored token undergoes is being
// revoked, and the two methods for that are separate because they answer
// different questions — one spends a single link, the other ends a chain.
type RefreshTokenRepository interface {
	// Create persists a newly issued token and returns it as the database
	// recorded it.
	Create(ctx context.Context, token *domain.RefreshToken) (*domain.RefreshToken, error)

	// FindByHash returns the token with this hash, or a not-found error.
	//
	// It takes the hash rather than the presented string, so the plaintext stops
	// at the use case and never reaches a query, a query log, or a span.
	FindByHash(ctx context.Context, hash domain.TokenHash) (*domain.RefreshToken, error)

	// Spend revokes a token that is still live and reports whether this call is
	// the one that did it.
	//
	// The bool is the whole point, and it is why this is not Save(token). Two
	// refreshes arriving with the same token must produce one new pair and one
	// rejection, and only the database can decide which is which — the caller
	// that gets false has been beaten to it, which is the same evidence of a
	// second holder that presenting an already-revoked token is.
	Spend(ctx context.Context, id domain.RefreshTokenID, revokedAt time.Time) (bool, error)

	// RevokeFamily ends every live token in a rotation chain.
	//
	// Idempotent, and deliberately silent about how many rows it touched: both
	// callers — a logout and a detected reuse — want the chain dead rather than
	// a count, and a client retrying a logout after a timeout must not be able
	// to tell that the first attempt got there.
	RevokeFamily(ctx context.Context, family domain.FamilyID, revokedAt time.Time) error
}
