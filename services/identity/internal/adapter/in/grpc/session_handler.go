package grpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/in"
)

// Login exchanges credentials for a token pair.
func (h *IdentityHandler) Login(
	ctx context.Context,
	req *identityv1.LoginRequest,
) (*identityv1.LoginResponse, error) {
	session, err := h.sessions.Login(ctx, in.LoginCommand{
		Email:    req.GetEmail(),
		Password: req.GetPassword(),
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &identityv1.LoginResponse{
		Tokens: toTokenPairProto(session.Tokens),
		User:   toProto(session.User),
	}, nil
}

// RefreshToken exchanges a refresh token for a new pair.
func (h *IdentityHandler) RefreshToken(
	ctx context.Context,
	req *identityv1.RefreshTokenRequest,
) (*identityv1.RefreshTokenResponse, error) {
	tokens, err := h.sessions.Refresh(ctx, req.GetRefreshToken())
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &identityv1.RefreshTokenResponse{Tokens: toTokenPairProto(tokens)}, nil
}

// Logout revokes the presented token's rotation chain.
func (h *IdentityHandler) Logout(
	ctx context.Context,
	req *identityv1.LogoutRequest,
) (*identityv1.LogoutResponse, error) {
	if err := h.sessions.Logout(ctx, req.GetRefreshToken()); err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &identityv1.LogoutResponse{}, nil
}

// toTokenPairProto maps the pair onto the wire.
//
// Reveal appears twice here, and this is the one place in the service where it
// should: the values were kept unprintable precisely so that handing them to the
// caller is an explicit act rather than something a %v could do by accident.
func toTokenPairProto(pair in.TokenPair) *identityv1.TokenPair {
	return &identityv1.TokenPair{
		AccessToken:           pair.AccessToken.Reveal(),
		RefreshToken:          pair.RefreshToken.Reveal(),
		AccessTokenExpiresAt:  timestamppb.New(pair.AccessTokenExpiresAt),
		RefreshTokenExpiresAt: timestamppb.New(pair.RefreshTokenExpiresAt),
	}
}
