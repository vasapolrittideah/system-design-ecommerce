// Package grpc is the driving adapter: it maps ecommerce.identity.v1 messages
// onto use case commands and back, and nothing else.
//
// Two absences are deliberate. It validates nothing, because the constraints are
// declared in the proto and enforced by an interceptor; and it constructs no
// errors, because every failure arrives already classified and leaves through
// errorx.ToGRPC.
package grpc

import (
	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/in"
)

// IdentityHandler serves IdentityService.
//
// One struct for the whole service because the generated interface is one, and
// the use cases behind it are two: users and sessions are separate slices here
// even though the proto presents them together.
//
// Embedding the generated Unimplemented struct lets a new RPC be added to the
// proto without breaking the build here: the method answers Unimplemented until
// someone writes it.
type IdentityHandler struct {
	identityv1.UnimplementedIdentityServiceServer

	users    in.UserUseCase
	sessions in.SessionUseCase
}

var _ identityv1.IdentityServiceServer = (*IdentityHandler)(nil)

// NewIdentityHandler builds the handler over the driving ports.
func NewIdentityHandler(users in.UserUseCase, sessions in.SessionUseCase) *IdentityHandler {
	return &IdentityHandler{users: users, sessions: sessions}
}
