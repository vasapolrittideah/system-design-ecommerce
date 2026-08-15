package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/app"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/out/mocks"
)

// The mocks assert their own expectations on cleanup, so a call that was set up
// and never made fails the test — which is how "no hash was computed" below is
// checked without asserting on a counter.
func setup(t *testing.T) (*mocks.MockUserRepository, *mocks.MockPasswordHasher, *app.UserService) {
	t.Helper()

	users := mocks.NewMockUserRepository(t)
	hasher := mocks.NewMockPasswordHasher(t)

	return users, hasher, app.NewUserService(users, hasher)
}

func TestRegister(t *testing.T) {
	t.Run("normalises the address before it is stored", func(t *testing.T) {
		users, hasher, service := setup(t)

		hasher.EXPECT().Hash("correct horse battery staple").Return("argon2id-hash", nil).Once()
		users.EXPECT().
			Create(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, user *domain.User) (*domain.User, error) {
				if got := user.Email(); got != "ada@example.com" {
					t.Errorf("Create() got email %q, want the normalised form", got)
				}

				if user.PasswordHash() != "argon2id-hash" {
					t.Errorf("Create() got hash %q, want the hasher's output", user.PasswordHash())
				}

				return user, nil
			}).
			Once()

		user, err := service.Register(context.Background(), in.RegisterCommand{
			Email:    "  Ada@Example.COM ",
			Password: "correct horse battery staple",
		})
		if err != nil {
			t.Fatalf("Register() error = %v, want nil", err)
		}

		if user.Email() != "ada@example.com" {
			t.Errorf("Register() email = %q, want the normalised form", user.Email())
		}
	})

	t.Run("a malformed address costs no hashing", func(t *testing.T) {
		// Hashing is deliberately expensive. Doing it before the cheap
		// rejection would let anyone who can send a malformed email spend it.
		// Neither mock is given an expectation, so any call here fails.
		_, _, service := setup(t)

		_, err := service.Register(context.Background(), in.RegisterCommand{
			Email:    "not-an-address",
			Password: "correct horse battery staple",
		})
		if err == nil {
			t.Fatal("Register() error = nil, want an error")
		}

		if got := errorx.KindOf(err); got != errorx.KindInvalidInput {
			t.Errorf("KindOf() = %q, want %q", got, errorx.KindInvalidInput)
		}
	})

	t.Run("a hasher failure is internal, not the caller's fault", func(t *testing.T) {
		_, hasher, service := setup(t)

		broken := errors.New("out of memory")
		hasher.EXPECT().Hash(mock.Anything).Return("", broken).Once()

		_, err := service.Register(context.Background(), in.RegisterCommand{
			Email:    "ada@example.com",
			Password: "correct horse battery staple",
		})

		if got := errorx.KindOf(err); got != errorx.KindInternal {
			t.Errorf("KindOf() = %q, want %q", got, errorx.KindInternal)
		}

		// The cause stays reachable, so the access log keeps the whole chain
		// even though the client is told nothing but "internal".
		if !errors.Is(err, broken) {
			t.Errorf("errors.Is(err, cause) = false, want true")
		}
	})

	t.Run("a duplicate email reaches the caller as the repository classified it", func(t *testing.T) {
		users, hasher, service := setup(t)

		conflict := errorx.New(errorx.KindConflict, "email is already registered").
			WithReason("EMAIL_ALREADY_REGISTERED")

		hasher.EXPECT().Hash(mock.Anything).Return("argon2id-hash", nil).Once()
		users.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, conflict).Once()

		_, err := service.Register(context.Background(), in.RegisterCommand{
			Email:    "ada@example.com",
			Password: "correct horse battery staple",
		})

		// The use case must not reclassify it: a conflict answered as Internal
		// tells the client to retry something that will never succeed.
		if got := errorx.KindOf(err); got != errorx.KindConflict {
			t.Errorf("KindOf() = %q, want %q", got, errorx.KindConflict)
		}

		if got := errorx.Reason(err); got != "EMAIL_ALREADY_REGISTERED" {
			t.Errorf("Reason() = %q, want %q", got, "EMAIL_ALREADY_REGISTERED")
		}
	})
}

func TestGetUser(t *testing.T) {
	const id = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

	t.Run("passes the parsed id through", func(t *testing.T) {
		users, _, service := setup(t)

		want := domain.Reconstitute(domain.Snapshot{ID: id, Email: "ada@example.com"})
		users.EXPECT().FindByID(mock.Anything, domain.UserID(id)).Return(want, nil).Once()

		got, err := service.GetUser(context.Background(), id)
		if err != nil {
			t.Fatalf("GetUser() error = %v, want nil", err)
		}

		if got.ID() != want.ID() {
			t.Errorf("GetUser() = %q, want %q", got.ID(), want.ID())
		}
	})

	t.Run("a malformed id never reaches the repository", func(t *testing.T) {
		// Unchecked, it would arrive as a pgx parse failure and be reported as
		// Internal — which says this service is broken when the caller made a
		// typo, and puts a client bug into the error rate that pages someone.
		_, _, service := setup(t)

		_, err := service.GetUser(context.Background(), "nonsense")
		if err == nil {
			t.Fatal("GetUser() error = nil, want an error")
		}

		if got := errorx.KindOf(err); got != errorx.KindInvalidInput {
			t.Errorf("KindOf() = %q, want %q", got, errorx.KindInvalidInput)
		}
	})
}

func TestGetUsersByIDs(t *testing.T) {
	const (
		first  = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
		second = "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
	)

	t.Run("fewer users than ids is not an error", func(t *testing.T) {
		// This is the read the Composition API fans out to fill a screen. One
		// deleted user must degrade the screen, not fail it.
		users, _, service := setup(t)

		found := []*domain.User{domain.Reconstitute(domain.Snapshot{ID: first})}
		users.EXPECT().
			FindByIDs(mock.Anything, []domain.UserID{first, second}).
			Return(found, nil).
			Once()

		got, err := service.GetUsersByIDs(context.Background(), []string{first, second})
		if err != nil {
			t.Fatalf("GetUsersByIDs() error = %v, want nil", err)
		}

		if len(got) != 1 {
			t.Errorf("GetUsersByIDs() returned %d users, want 1", len(got))
		}
	})

	t.Run("one malformed id fails the whole call", func(t *testing.T) {
		// Unlike a missing id, a malformed one is a caller that built a bad
		// request, and answering part of it hides the bug.
		_, _, service := setup(t)

		_, err := service.GetUsersByIDs(context.Background(), []string{first, "nonsense"})
		if err == nil {
			t.Fatal("GetUsersByIDs() error = nil, want an error")
		}

		if got := errorx.KindOf(err); got != errorx.KindInvalidInput {
			t.Errorf("KindOf() = %q, want %q", got, errorx.KindInvalidInput)
		}
	})
}
