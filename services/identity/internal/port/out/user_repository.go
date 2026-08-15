// Package out declares the driven ports: what the identity service needs from
// the world outside it, in the vocabulary of the use cases that call them rather
// than of the adapters that implement them.
package out

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
)

// UserRepository stores and reads user aggregates.
//
// Every method takes a context because the adapter pulls the current
// transaction off it — a use case that wraps several calls in txmanager.Do gets
// them in one transaction without any of these signatures changing.
type UserRepository interface {
	// Create persists a user that has never been stored and returns it as the
	// database recorded it, which is where created_at and updated_at come from.
	//
	// A duplicate email comes back as a conflict, so the caller does not check
	// first: the UNIQUE constraint is the guard, and checking then inserting
	// leaves a window where two requests both find nothing and both proceed.
	Create(ctx context.Context, user *domain.User) (*domain.User, error)

	// FindByID returns the user, or a not-found error when there is none.
	FindByID(ctx context.Context, id domain.UserID) (*domain.User, error)

	// FindByIDs returns the users that exist, in no guaranteed order and
	// possibly fewer than were asked for.
	//
	// A missing id is not an error: this is the read the Composition API fans
	// out to fill a screen, and one deleted user must not fail the screen.
	FindByIDs(ctx context.Context, ids []domain.UserID) ([]*domain.User, error)
}
