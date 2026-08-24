package domain_test

import (
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
)

func newUser(t *testing.T) *domain.User {
	t.Helper()

	email, err := domain.NewEmail("someone@example.com")
	if err != nil {
		t.Fatalf("NewEmail() error = %v, want nil", err)
	}

	user, err := domain.NewUser(email, "argon2id$hash")
	if err != nil {
		t.Fatalf("NewUser() error = %v, want nil", err)
	}

	return user
}

func TestNewUserRaisesUserRegistered(t *testing.T) {
	user := newUser(t)

	pulled := user.PullEvents()
	if len(pulled) != 1 {
		t.Fatalf("PullEvents() returned %d events, want 1", len(pulled))
	}

	event, ok := pulled[0].(domain.UserRegistered)
	if !ok {
		t.Fatalf("PullEvents() returned %T, want domain.UserRegistered", pulled[0])
	}
	if event.EventName() != "UserRegistered" {
		t.Errorf("EventName() = %q, want %q", event.EventName(), "UserRegistered")
	}
	if event.UserID != user.ID() {
		t.Errorf("event user id = %v, want %v", event.UserID, user.ID())
	}
	if event.Email != user.Email() {
		t.Errorf("event email = %v, want %v", event.Email, user.Email())
	}
	// What the account was created with, which a consumer reads as the roles at
	// registration and never as the roles now.
	if len(event.Roles) != 1 || event.Roles[0] != domain.RoleCustomer {
		t.Errorf("event roles = %v, want [%v]", event.Roles, domain.RoleCustomer)
	}
}

func TestPullEventsEmptiesTheAggregate(t *testing.T) {
	user := newUser(t)

	if len(user.PullEvents()) != 1 {
		t.Fatal("first PullEvents() returned nothing, want the registration")
	}

	// A repository that persists the same aggregate twice must not publish the
	// registration twice.
	if pulled := user.PullEvents(); len(pulled) != 0 {
		t.Errorf("second PullEvents() returned %d events, want 0", len(pulled))
	}
}

func TestReconstituteUserRaisesNothing(t *testing.T) {
	user := newUser(t)
	snapshot := user.Snapshot()

	// The events belong to the change that was just made. A user read back from
	// storage has made none, and announcing its registration again on every
	// sign-in would be a fact the system hears twice.
	if pulled := domain.ReconstituteUser(snapshot).PullEvents(); len(pulled) != 0 {
		t.Errorf("PullEvents() returned %d events, want 0", len(pulled))
	}
}
