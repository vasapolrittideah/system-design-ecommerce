// Package in declares the driving ports: what can be asked of this service,
// stated without reference to how the asking arrives. A gRPC handler maps its
// request into one of the commands below and calls the interface.
package in

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
)

// RegisterCommand is a request to create an account.
//
// The fields are the raw strings that arrived, not domain types: turning them
// into an Email is a rule, and rules are not the adapter's to apply.
type RegisterCommand struct {
	Email string

	// Password is plaintext, and is the reason this struct is never logged or
	// attached to a span. It lives for the duration of one hash.
	Password string
}

// UserUseCase is everything that can be done to users in this slice.
//
// One interface rather than one per use case, because these share a lifetime and
// a dependency set — a repository and a hasher. Sign-in does not, which is why
// it is [SessionUseCase] instead of three more methods here.
type UserUseCase interface {
	// Register creates a user and returns them. It mints no token: registering
	// and signing in are separate acts.
	Register(ctx context.Context, cmd RegisterCommand) (*domain.User, error)

	// GetUser reads one user by id, and reports not found rather than nil.
	GetUser(ctx context.Context, id string) (*domain.User, error)

	// GetUsersByIDs reads many, returning only the ones that exist.
	GetUsersByIDs(ctx context.Context, ids []string) ([]*domain.User, error)
}
