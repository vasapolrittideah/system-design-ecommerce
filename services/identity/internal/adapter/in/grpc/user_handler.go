// Package grpc is the driving adapter: it maps ecommerce.identity.v1 messages
// onto use case commands and back, and nothing else.
//
// Two things are deliberately absent. There is no validation — the constraints
// are declared in the proto with protovalidate and enforced by one interceptor,
// so a handler that checks its own input is duplicating a rule that has an
// owner. And there is no error construction: every failure arrives already
// classified and leaves through errorx.ToGRPC, which is what keeps one kind
// from being answered as two different codes depending on which handler it
// passed through.
package grpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/in"
)

// UserHandler serves IdentityService.
//
// Embedding the generated Unimplemented struct is what lets a new RPC be added
// to the proto without breaking the build here — the method answers
// Unimplemented until someone writes it, rather than the package failing to
// compile against its own contract.
type UserHandler struct {
	identityv1.UnimplementedIdentityServiceServer

	users in.UserUseCase
}

var _ identityv1.IdentityServiceServer = (*UserHandler)(nil)

// NewUserHandler builds the handler over the driving port.
func NewUserHandler(users in.UserUseCase) *UserHandler {
	return &UserHandler{users: users}
}

// Register creates an account.
func (h *UserHandler) Register(
	ctx context.Context,
	req *identityv1.RegisterRequest,
) (*identityv1.RegisterResponse, error) {
	user, err := h.users.Register(ctx, in.RegisterCommand{
		Email:    req.GetEmail(),
		Password: req.GetPassword(),
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &identityv1.RegisterResponse{User: toProto(user)}, nil
}

// GetUser reads one user.
//
// There is no authorization check here, and its absence is a decision rather
// than an omission: this RPC is reached over east-west gRPC, where identity was
// settled two hops earlier at the edge, and its callers are the Composition API
// filling a screen and other services resolving a user they already hold the id
// of. The rule that a person may only read themselves belongs to the endpoint
// that has a person behind it, and arrives with the Composition API.
func (h *UserHandler) GetUser(
	ctx context.Context,
	req *identityv1.GetUserRequest,
) (*identityv1.GetUserResponse, error) {
	user, err := h.users.GetUser(ctx, req.GetId())
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &identityv1.GetUserResponse{User: toProto(user)}, nil
}

// GetUsersByIDs reads many, returning only the users that exist.
func (h *UserHandler) GetUsersByIDs(
	ctx context.Context,
	req *identityv1.GetUsersByIDsRequest,
) (*identityv1.GetUsersByIDsResponse, error) {
	users, err := h.users.GetUsersByIDs(ctx, req.GetIds())
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	message := make([]*identityv1.User, 0, len(users))
	for _, user := range users {
		message = append(message, toProto(user))
	}

	return &identityv1.GetUsersByIDsResponse{Users: message}, nil
}

// toProto maps the aggregate to what this service tells everyone else.
//
// The password hash and the optimistic-locking version have no field on the
// message and are dropped here. That is the boundary doing its job: a field on
// ecommerce.identity.v1.User is a promise to every caller, and the two omitted
// here are internal facts that no caller should be able to come to depend on.
func toProto(user *domain.User) *identityv1.User {
	roles := make([]string, 0, len(user.Roles()))
	for _, role := range user.Roles() {
		roles = append(roles, string(role))
	}

	return &identityv1.User{
		Id:        user.ID().String(),
		Email:     user.Email().String(),
		Roles:     roles,
		CreatedAt: timestamppb.New(user.CreatedAt()),
		UpdatedAt: timestamppb.New(user.UpdatedAt()),
	}
}
