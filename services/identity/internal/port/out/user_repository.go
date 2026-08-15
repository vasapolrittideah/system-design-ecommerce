// Package out declares the driven ports: what the identity service needs from
// the world outside it.
//
// The interfaces are declared here, next to the use cases that call them,
// rather than next to the adapters that implement them. That is the whole point
// of the direction — app depends on this package, adapters depend on this
// package, and nothing in app depends on pgx or argon2.
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
	// A duplicate email is a conflict, not an error the caller has to detect
	// beforehand: the UNIQUE constraint is the guard, and the adapter maps the
	// violation. Checking first and inserting second leaves a window where two
	// requests both find nothing and both proceed.
	Create(ctx context.Context, user *domain.User) (*domain.User, error)

	// FindByID returns the user, or a not-found error when there is none.
	FindByID(ctx context.Context, id domain.UserID) (*domain.User, error)

	// FindByIDs returns the users that exist, in no guaranteed order and
	// possibly fewer than were asked for.
	//
	// A missing id is not an error. This is the read the Composition API fans
	// out to fill a screen, and one deleted user must not fail the whole
	// screen.
	FindByIDs(ctx context.Context, ids []domain.UserID) ([]*domain.User, error)
}
