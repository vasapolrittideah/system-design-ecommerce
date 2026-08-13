package server_test

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/grpc/metadata"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/server"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

func TestMetadataIdentity(t *testing.T) {
	tests := []struct {
		name      string
		md        metadata.MD
		wantFound bool
		wantUser  string
		wantRoles []string
	}{
		{
			name:      "user and comma separated roles",
			md:        metadata.Pairs(grpcx.MetadataUserID, "u-1", grpcx.MetadataUserRoles, "customer,admin"),
			wantFound: true,
			wantUser:  "u-1",
			wantRoles: []string{"customer", "admin"},
		},
		{
			// A caller building metadata by hand tends to append one key per
			// role rather than joining them, and both shapes reach a service.
			name:      "repeated role keys",
			md:        metadata.Pairs(grpcx.MetadataUserID, "u-2", grpcx.MetadataUserRoles, "customer", grpcx.MetadataUserRoles, "admin"),
			wantFound: true,
			wantUser:  "u-2",
			wantRoles: []string{"customer", "admin"},
		},
		{
			name:      "padded roles are trimmed and empties dropped",
			md:        metadata.Pairs(grpcx.MetadataUserID, "u-3", grpcx.MetadataUserRoles, " customer , , admin "),
			wantFound: true,
			wantUser:  "u-3",
			wantRoles: []string{"customer", "admin"},
		},
		{
			name:      "user without roles",
			md:        metadata.Pairs(grpcx.MetadataUserID, "u-4"),
			wantFound: true,
			wantUser:  "u-4",
		},
		{
			name: "no identity metadata at all",
			md:   metadata.Pairs("x-unrelated", "value"),
		},
		{
			name: "empty metadata",
			md:   metadata.MD{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), tt.md)

			ctx, err := server.MetadataIdentity(ctx, "/grpcx.test.v1.TestService/GetEcho")
			if err != nil {
				t.Fatalf("MetadataIdentity returned an error: %v", err)
			}

			id, ok := grpcx.IdentityFrom(ctx)
			if ok != tt.wantFound {
				t.Fatalf("identity found = %v, want %v", ok, tt.wantFound)
			}
			if !tt.wantFound {
				return
			}

			if id.UserID != tt.wantUser {
				t.Errorf("UserID = %q, want %q", id.UserID, tt.wantUser)
			}
			if !slices.Equal(id.Roles, tt.wantRoles) {
				t.Errorf("Roles = %v, want %v", id.Roles, tt.wantRoles)
			}
			// The same user has to reach the logs, or a request cannot be
			// attributed to anyone after the fact.
			if got := logger.UserID(ctx); got != tt.wantUser {
				t.Errorf("logger.UserID = %q, want %q", got, tt.wantUser)
			}
		})
	}
}

// The default authenticator deliberately admits callers with no user behind
// them: outbox relays, saga timeout workers, and plain service-to-service reads
// all arrive that way, and rejecting them would break the system while
// protecting nothing.
func TestMetadataIdentityAdmitsUnauthenticatedCallers(t *testing.T) {
	ctx, err := server.MetadataIdentity(context.Background(), "/grpcx.test.v1.TestService/GetEcho")
	if err != nil {
		t.Fatalf("MetadataIdentity returned an error: %v", err)
	}
	if _, ok := grpcx.IdentityFrom(ctx); ok {
		t.Error("an identity was set for a call carrying no metadata")
	}
}
