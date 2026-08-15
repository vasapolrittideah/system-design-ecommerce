package grpc_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/in/grpc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/in"
)

// stubSessionUseCase records what the handler asked for and answers with what
// the test wants. Hand-written for the same reason as stubUseCase: only driven
// ports are generated, and what these tests assert on is the mapping.
type stubSessionUseCase struct {
	command      in.LoginCommand
	refreshToken string

	session in.Session
	tokens  in.TokenPair
	err     error
}

func (s *stubSessionUseCase) Login(_ context.Context, cmd in.LoginCommand) (in.Session, error) {
	s.command = cmd

	return s.session, s.err
}

func (s *stubSessionUseCase) Refresh(_ context.Context, token string) (in.TokenPair, error) {
	s.refreshToken = token

	return s.tokens, s.err
}

func (s *stubSessionUseCase) Logout(_ context.Context, token string) error {
	s.refreshToken = token

	return s.err
}

func newTokenPair() in.TokenPair {
	return in.TokenPair{
		AccessToken:           "access-token",
		AccessTokenExpiresAt:  time.Now().Add(15 * time.Minute),
		RefreshToken:          "refresh-token",
		RefreshTokenExpiresAt: time.Now().Add(720 * time.Hour),
	}
}

func TestLoginMapsRequestAndResponse(t *testing.T) {
	stub := &stubSessionUseCase{session: in.Session{Tokens: newTokenPair(), User: newUser(t)}}
	handler := adapter.NewIdentityHandler(nil, stub)

	resp, err := handler.Login(context.Background(), &identityv1.LoginRequest{
		Email:    "ada@example.com",
		Password: "hunter2",
	})
	if err != nil {
		t.Fatalf("Login() error = %v, want nil", err)
	}

	if stub.command.Email != "ada@example.com" || stub.command.Password != "hunter2" {
		t.Errorf("Login() passed %+v, want the request's credentials", stub.command)
	}

	// Both values have to survive the mapping. They are unprintable types
	// precisely so that only a deliberate Reveal gets them onto the wire, and a
	// mapping that forgot one would hand back an empty string rather than fail.
	if resp.GetTokens().GetAccessToken() != "access-token" {
		t.Errorf("access_token = %q, want the signed value", resp.GetTokens().GetAccessToken())
	}

	if resp.GetTokens().GetRefreshToken() != "refresh-token" {
		t.Errorf("refresh_token = %q, want the minted value", resp.GetTokens().GetRefreshToken())
	}

	if resp.GetUser().GetEmail() != "ada@example.com" {
		t.Errorf("user.email = %q, want the signed-in user", resp.GetUser().GetEmail())
	}
}

func TestLoginFailureKeepsItsCode(t *testing.T) {
	// The handler must not classify this itself. The use case already decided
	// that a credential failure is Unauthenticated and that an outage is not.
	stub := &stubSessionUseCase{
		err: errorx.New(errorx.KindUnauthenticated, "invalid credentials").
			WithReason("INVALID_CREDENTIALS"),
	}
	handler := adapter.NewIdentityHandler(nil, stub)

	_, err := handler.Login(context.Background(), &identityv1.LoginRequest{})
	if got := status.Code(err); got != codes.Unauthenticated {
		t.Errorf("status code = %s, want %s", got, codes.Unauthenticated)
	}

	if got := errorx.Reason(err); got != "INVALID_CREDENTIALS" {
		t.Errorf("Reason() = %q, want INVALID_CREDENTIALS", got)
	}
}

func TestRefreshTokenMapsRequestAndResponse(t *testing.T) {
	stub := &stubSessionUseCase{tokens: newTokenPair()}
	handler := adapter.NewIdentityHandler(nil, stub)

	resp, err := handler.RefreshToken(context.Background(), &identityv1.RefreshTokenRequest{
		RefreshToken: "presented",
	})
	if err != nil {
		t.Fatalf("RefreshToken() error = %v, want nil", err)
	}

	if stub.refreshToken != "presented" {
		t.Errorf("RefreshToken() passed %q, want the presented token", stub.refreshToken)
	}

	if resp.GetTokens().GetRefreshToken() != "refresh-token" {
		t.Errorf("refresh_token = %q, want the successor's value", resp.GetTokens().GetRefreshToken())
	}

	// The response carries no user by design — nothing on the screen that
	// refreshed has changed, and RefreshTokenResponse has no field for one.
	if resp.GetTokens().GetAccessTokenExpiresAt() == nil {
		t.Error("access_token_expires_at is unset, want the absolute expiry")
	}
}

func TestLogoutIsEmptyOnSuccess(t *testing.T) {
	stub := &stubSessionUseCase{}
	handler := adapter.NewIdentityHandler(nil, stub)

	if _, err := handler.Logout(context.Background(), &identityv1.LogoutRequest{
		RefreshToken: "presented",
	}); err != nil {
		t.Fatalf("Logout() error = %v, want nil", err)
	}

	if stub.refreshToken != "presented" {
		t.Errorf("Logout() passed %q, want the presented token", stub.refreshToken)
	}
}
