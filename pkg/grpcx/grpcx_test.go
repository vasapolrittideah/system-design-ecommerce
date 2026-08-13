package grpcx_test

import (
	"context"
	"slices"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
)

func TestIdentityRoundTrip(t *testing.T) {
	want := grpcx.Identity{UserID: "u-1", Roles: []string{"customer", "beta"}}

	got, ok := grpcx.IdentityFrom(grpcx.IdentityInto(context.Background(), want))
	if !ok {
		t.Fatal("IdentityFrom reported no identity after IdentityInto")
	}
	if got.UserID != want.UserID {
		t.Errorf("UserID = %q, want %q", got.UserID, want.UserID)
	}
	if !slices.Equal(got.Roles, want.Roles) {
		t.Errorf("Roles = %v, want %v", got.Roles, want.Roles)
	}
}

// An absent identity has to be distinguishable from an empty one: a relay or a
// consumer runs with no user behind it, and code that requires one must be able
// to tell that apart from a user whose ID happens to be blank.
func TestIdentityFromWithoutIdentity(t *testing.T) {
	if _, ok := grpcx.IdentityFrom(context.Background()); ok {
		t.Error("IdentityFrom reported an identity on a bare context")
	}
}
