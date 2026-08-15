package grpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/in"
)

// Register creates an account.
func (h *IdentityHandler) Register(
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
func (h *IdentityHandler) GetUser(
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
func (h *IdentityHandler) GetUsersByIDs(
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
