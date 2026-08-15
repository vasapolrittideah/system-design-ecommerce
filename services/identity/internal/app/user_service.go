// Package app holds the use cases: the orchestration between a request and the
// domain rules that answer it. There is deliberately very little here — an `if`
// in this package that encodes a business policy is a rule that escaped the
// domain, where it could have been tested without a mock.
package app

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/out"
)

// UserService implements the user use cases over a repository and a hasher.
type UserService struct {
	users  out.UserRepository
	hasher out.PasswordHasher
}

// Compile-time proof that the driving port is satisfied. Without it the failure
// surfaces in bootstrap, naming the wiring rather than the missing method.
var _ in.UserUseCase = (*UserService)(nil)

// NewUserService wires the use cases to their driven ports.
func NewUserService(users out.UserRepository, hasher out.PasswordHasher) *UserService {
	return &UserService{users: users, hasher: hasher}
}

// Register creates an account.
//
// The order is the point: check the address, then hash, then write. Hashing is
// tens of milliseconds of deliberate work, and doing it before the cheap
// rejection lets anyone who can send a malformed email consume that time.
//
// There is deliberately no "does this email already exist" read: the UNIQUE
// constraint is the guard, and a check followed by an insert has a window
// between them where duplicate accounts come from.
func (s *UserService) Register(ctx context.Context, cmd in.RegisterCommand) (*domain.User, error) {
	email, err := domain.NewEmail(cmd.Email)
	if err != nil {
		return nil, err
	}

	hash, err := s.hasher.Hash(cmd.Password)
	if err != nil {
		// Wrapped rather than returned bare: a hasher failure is a broken
		// process, not something the caller did, and Internal is what says so.
		return nil, errorx.Wrap(err, errorx.KindInternal, "hash password")
	}

	user, err := domain.NewUser(email, hash)
	if err != nil {
		return nil, err
	}

	return s.users.Create(ctx, user)
}

// GetUser reads one user.
func (s *UserService) GetUser(ctx context.Context, id string) (*domain.User, error) {
	userID, err := domain.ParseUserID(id)
	if err != nil {
		return nil, err
	}

	return s.users.FindByID(ctx, userID)
}

// GetUsersByIDs reads many.
//
// One malformed id fails the whole call, where one *missing* id does not: the
// first is a bad request, the second is the ordinary case of a screen naming a
// user who has since been deleted.
func (s *UserService) GetUsersByIDs(ctx context.Context, ids []string) ([]*domain.User, error) {
	userIDs := make([]domain.UserID, 0, len(ids))

	for _, id := range ids {
		userID, err := domain.ParseUserID(id)
		if err != nil {
			return nil, err
		}

		userIDs = append(userIDs, userID)
	}

	return s.users.FindByIDs(ctx, userIDs)
}
