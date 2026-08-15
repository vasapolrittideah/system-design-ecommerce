package grpc_test

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/in/grpc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/in"
)

// stubUseCase records what the handler asked for and answers with what the test
// wants. Hand-written rather than generated because only driven ports are listed
// in .mockery.yml, and what these tests assert on is the mapping.
type stubUseCase struct {
	command in.RegisterCommand
	id      string
	ids     []string

	user  *domain.User
	users []*domain.User
	err   error
}

func (s *stubUseCase) Register(_ context.Context, cmd in.RegisterCommand) (*domain.User, error) {
	s.command = cmd

	return s.user, s.err
}

func (s *stubUseCase) GetUser(_ context.Context, id string) (*domain.User, error) {
	s.id = id

	return s.user, s.err
}

func (s *stubUseCase) GetUsersByIDs(_ context.Context, ids []string) ([]*domain.User, error) {
	s.ids = ids

	return s.users, s.err
}

func newUser(t *testing.T) *domain.User {
	t.Helper()

	user, err := domain.NewUser("ada@example.com", "argon2id-hash")
	if err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}

	return user
}

func TestRegisterMapsRequestAndResponse(t *testing.T) {
	stub := &stubUseCase{user: newUser(t)}
	handler := adapter.NewUserHandler(stub)

	resp, err := handler.Register(context.Background(), &identityv1.RegisterRequest{
		Email:    "Ada@example.com",
		Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("Register() error = %v, want nil", err)
	}

	// The handler passes the input through untouched: normalising it here would
	// put a rule in the adapter, where the next transport would not have it.
	if stub.command.Email != "Ada@example.com" {
		t.Errorf("command email = %q, want the request's value", stub.command.Email)
	}

	if stub.command.Password != "correct horse battery staple" {
		t.Errorf("command password = %q, want the request's value", stub.command.Password)
	}

	if resp.GetUser().GetId() != stub.user.ID().String() {
		t.Errorf("response id = %q, want %q", resp.GetUser().GetId(), stub.user.ID())
	}

	if roles := resp.GetUser().GetRoles(); len(roles) != 1 || roles[0] != string(domain.RoleCustomer) {
		t.Errorf("response roles = %v, want [%q]", roles, domain.RoleCustomer)
	}
}

// Nothing on the outward message can carry the hash — there is no field for it
// — so what this guards is the day someone adds one for convenience.
func TestRegisterResponseCarriesNoSecret(t *testing.T) {
	stub := &stubUseCase{user: newUser(t)}
	handler := adapter.NewUserHandler(stub)

	resp, err := handler.Register(context.Background(), &identityv1.RegisterRequest{
		Email:    "ada@example.com",
		Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("Register() error = %v, want nil", err)
	}

	rendered := resp.GetUser().String()
	for _, secret := range []string{"argon2id-hash", "correct horse battery staple"} {
		if strings.Contains(rendered, secret) {
			t.Errorf("response %q contains %q", rendered, secret)
		}
	}
}

func TestHandlerMapsErrorKindsToCodes(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		want   codes.Code
		reason string
	}{
		{
			name:   "domain validation error",
			err:    domain.ValidationError{Field: "email", Message: "not a valid address"},
			want:   codes.InvalidArgument,
			reason: "INVALID_INPUT",
		},
		{
			name: "duplicate email",
			err: errorx.New(errorx.KindConflict, "email is already registered").
				WithReason("EMAIL_ALREADY_REGISTERED"),
			want:   codes.FailedPrecondition,
			reason: "EMAIL_ALREADY_REGISTERED",
		},
		{
			name:   "missing user",
			err:    errorx.New(errorx.KindNotFound, "user not found").WithReason("USER_NOT_FOUND"),
			want:   codes.NotFound,
			reason: "USER_NOT_FOUND",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubUseCase{err: tt.err}
			handler := adapter.NewUserHandler(stub)

			_, err := handler.GetUser(context.Background(), &identityv1.GetUserRequest{Id: "any"})
			if err == nil {
				t.Fatal("GetUser() error = nil, want an error")
			}

			// Parsing the id belongs to the use case, not here: the handler
			// hands over whatever arrived.
			if stub.id != "any" {
				t.Errorf("use case got id %q, want the request's value", stub.id)
			}

			if got := status.Code(err); got != tt.want {
				t.Errorf("status code = %s, want %s", got, tt.want)
			}

			// The reason is what a client branches on: a 409 alone cannot say
			// whether an email was taken or an aggregate was in a bad state.
			if got := errorx.Reason(err); got != tt.reason {
				t.Errorf("Reason() = %q, want %q", got, tt.reason)
			}
		})
	}
}

// An internal failure must reach the client scrubbed, while the error the
// handler returns still wraps the original for the access log.
func TestInternalErrorIsNotLeaked(t *testing.T) {
	const leak = "dial tcp 10.0.3.7:5432: connection refused"

	handler := adapter.NewUserHandler(&stubUseCase{
		err: errorx.New(errorx.KindInternal, "%s", leak),
	})

	_, err := handler.GetUser(context.Background(), &identityv1.GetUserRequest{Id: "any"})
	if err == nil {
		t.Fatal("GetUser() error = nil, want an error")
	}

	if message := status.Convert(err).Message(); strings.Contains(message, "10.0.3.7") {
		t.Errorf("status message %q leaks the internal error", message)
	}
}

func TestGetUsersByIDsReturnsWhatTheUseCaseFound(t *testing.T) {
	stub := &stubUseCase{users: []*domain.User{newUser(t)}}
	handler := adapter.NewUserHandler(stub)

	ids := []string{"6ba7b810-9dad-11d1-80b4-00c04fd430c8", "6ba7b811-9dad-11d1-80b4-00c04fd430c8"}

	resp, err := handler.GetUsersByIDs(context.Background(), &identityv1.GetUsersByIDsRequest{Ids: ids})
	if err != nil {
		t.Fatalf("GetUsersByIDs() error = %v, want nil", err)
	}

	if len(stub.ids) != len(ids) {
		t.Errorf("use case got %d ids, want %d", len(stub.ids), len(ids))
	}

	// Fewer users than ids is the ordinary case and must not become an error on
	// the way out either.
	if len(resp.GetUsers()) != 1 {
		t.Errorf("response carries %d users, want 1", len(resp.GetUsers()))
	}
}
