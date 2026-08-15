// Package grpc is the driving adapter: it maps ecommerce.identity.v1 messages
// onto use case commands and back, and nothing else.
//
// Two absences are deliberate. It validates nothing, because the constraints are
// declared in the proto and enforced by an interceptor; and it constructs no
// errors, because every failure arrives already classified and leaves through
// errorx.ToGRPC.
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
// Embedding the generated Unimplemented struct lets a new RPC be added to the
// proto without breaking the build here: the method answers Unimplemented until
// someone writes it.
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
// The missing authorization check is a decision, not an omission: this RPC is
// reached over east-west gRPC by the Composition API and by services resolving
// an id they already hold. The rule that a person may only read themselves
// belongs to the endpoint that has a person behind it.
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
// The password hash and the version are dropped here, deliberately: a field on
// ecommerce.identity.v1.User is a promise to every caller, and neither is a fact
// anyone outside should be able to depend on.
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
