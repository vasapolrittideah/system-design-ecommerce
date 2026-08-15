package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/app"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/out"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/out/mocks"
)

const refreshTTL = 720 * time.Hour

type sessionDeps struct {
	users  *mocks.MockUserRepository
	tokens *mocks.MockRefreshTokenRepository
	hasher *mocks.MockPasswordHasher
	issuer *mocks.MockAccessTokenIssuer
	tx     *mocks.MockTxManager
}

// setupSession wires the service to mocks that assert their own expectations on
// cleanup, so a call that was set up and never made fails the test.
//
// The transaction manager runs its closure inline. That is the real behaviour a
// use case sees on the happy path, and it means the tests below exercise the
// rotation body rather than a stub standing in for it.
func setupSession(t *testing.T) (sessionDeps, *app.SessionService) {
	t.Helper()

	deps := sessionDeps{
		users:  mocks.NewMockUserRepository(t),
		tokens: mocks.NewMockRefreshTokenRepository(t),
		hasher: mocks.NewMockPasswordHasher(t),
		issuer: mocks.NewMockAccessTokenIssuer(t),
		tx:     mocks.NewMockTxManager(t),
	}

	service := app.NewSessionService(
		app.SessionConfig{RefreshTTL: refreshTTL},
		deps.users, deps.tokens, deps.hasher, deps.issuer, deps.tx,
	)

	return deps, service
}

// expectInlineTx makes Do run its closure and return whatever it returns.
func expectInlineTx(deps sessionDeps) {
	deps.tx.EXPECT().
		Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		}).
		Once()
}

func newTestUser(t *testing.T) *domain.User {
	t.Helper()

	user, err := domain.NewUser("ada@example.com", "argon2id-hash")
	if err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}

	return user
}

// echoCreate answers Create with the token it was given, which is what the real
// repository does apart from the timestamps.
func echoCreate(deps sessionDeps) {
	deps.tokens.EXPECT().
		Create(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, token *domain.RefreshToken) (*domain.RefreshToken, error) {
			return token, nil
		}).
		Once()
}

func expectIssuedAccessToken(deps sessionDeps, expiresAt time.Time) {
	deps.issuer.EXPECT().
		Issue(mock.Anything, mock.Anything).
		Return(out.AccessToken{Value: "access-token", ExpiresAt: expiresAt}, nil).
		Once()
}

func TestLogin(t *testing.T) {
	t.Run("returns a pair and the user", func(t *testing.T) {
		deps, service := setupSession(t)
		user := newTestUser(t)
		expiresAt := time.Now().Add(15 * time.Minute)

		deps.users.EXPECT().FindByEmail(mock.Anything, domain.Email("ada@example.com")).Return(user, nil).Once()
		deps.hasher.EXPECT().Verify(domain.PasswordHash("argon2id-hash"), "hunter2").Return(true, nil).Once()
		expectIssuedAccessToken(deps, expiresAt)
		echoCreate(deps)

		session, err := service.Login(context.Background(), in.LoginCommand{
			Email:    "  Ada@Example.COM ",
			Password: "hunter2",
		})
		if err != nil {
			t.Fatalf("Login() error = %v, want nil", err)
		}

		if session.User != user {
			t.Error("Login() did not return the user it verified")
		}

		if session.Tokens.AccessToken.Reveal() != "access-token" {
			t.Errorf("AccessToken = %q, want the signed value", session.Tokens.AccessToken.Reveal())
		}

		if session.Tokens.RefreshToken.Reveal() == "" {
			t.Error("RefreshToken is empty, want the minted plaintext")
		}

		// The chain's end is set from the TTL, and is what a client is told to
		// sign in again by.
		if got := time.Until(session.Tokens.RefreshTokenExpiresAt); got < refreshTTL-time.Minute {
			t.Errorf("RefreshTokenExpiresAt is %s away, want about %s", got, refreshTTL)
		}
	})

	t.Run("an unknown address still costs a verification", func(t *testing.T) {
		// The point of the expectation: without it, an early return would make
		// this call measurably faster than a wrong password, and the timing
		// would answer what the error deliberately does not.
		deps, service := setupSession(t)

		deps.users.EXPECT().
			FindByEmail(mock.Anything, mock.Anything).
			Return(nil, errorx.New(errorx.KindNotFound, "no user with that email")).
			Once()
		deps.hasher.EXPECT().Verify(domain.PasswordHash(""), "hunter2").Return(false, nil).Once()

		_, err := service.Login(context.Background(), in.LoginCommand{
			Email:    "nobody@example.com",
			Password: "hunter2",
		})
		assertInvalidCredentials(t, err)
	})

	t.Run("a wrong password is the same answer as an unknown address", func(t *testing.T) {
		deps, service := setupSession(t)

		deps.users.EXPECT().FindByEmail(mock.Anything, mock.Anything).Return(newTestUser(t), nil).Once()
		deps.hasher.EXPECT().Verify(mock.Anything, mock.Anything).Return(false, nil).Once()

		_, err := service.Login(context.Background(), in.LoginCommand{
			Email:    "ada@example.com",
			Password: "wrong",
		})
		assertInvalidCredentials(t, err)
	})

	t.Run("a malformed address never reaches the repository", func(t *testing.T) {
		_, service := setupSession(t)

		_, err := service.Login(context.Background(), in.LoginCommand{
			Email:    "not-an-address",
			Password: "hunter2",
		})
		assertInvalidCredentials(t, err)
	})

	t.Run("an unreachable repository stays internal", func(t *testing.T) {
		// The failure that must not be reclassified: answering 401 here tells
		// every client to sign in again during an outage, and keeps the outage
		// out of the error rate.
		deps, service := setupSession(t)

		deps.users.EXPECT().
			FindByEmail(mock.Anything, mock.Anything).
			Return(nil, errorx.New(errorx.KindInternal, "connection refused")).
			Once()

		_, err := service.Login(context.Background(), in.LoginCommand{
			Email:    "ada@example.com",
			Password: "hunter2",
		})
		if got := errorx.KindOf(err); got != errorx.KindInternal {
			t.Errorf("KindOf() = %q, want %q", got, errorx.KindInternal)
		}
	})
}

func assertInvalidCredentials(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("Login() error = nil, want an error")
	}

	if got := errorx.KindOf(err); got != errorx.KindUnauthenticated {
		t.Errorf("KindOf() = %q, want %q", got, errorx.KindUnauthenticated)
	}

	if got := errorx.Reason(err); got != "INVALID_CREDENTIALS" {
		t.Errorf("Reason() = %q, want INVALID_CREDENTIALS", got)
	}
}

// issueToken mints a live token the way the service would, so a refresh test
// has something with a real hash and family to present.
func issueToken(t *testing.T) (*domain.RefreshToken, domain.TokenValue) {
	t.Helper()

	token, value, err := domain.IssueRefreshToken(domain.NewUserID(), refreshTTL, time.Now())
	if err != nil {
		t.Fatalf("IssueRefreshToken() error = %v", err)
	}

	return token, value
}

func TestRefresh(t *testing.T) {
	t.Run("spends the presented token and stores its successor", func(t *testing.T) {
		deps, service := setupSession(t)
		token, value := issueToken(t)
		user := newTestUser(t)

		deps.tokens.EXPECT().
			FindByHash(mock.Anything, domain.HashRefreshToken(value.Reveal())).
			Return(token, nil).
			Once()
		deps.users.EXPECT().FindByID(mock.Anything, token.UserID()).Return(user, nil).Once()
		expectIssuedAccessToken(deps, time.Now().Add(15*time.Minute))
		expectInlineTx(deps)
		deps.tokens.EXPECT().Spend(mock.Anything, token.ID(), mock.Anything).Return(true, nil).Once()

		var successor *domain.RefreshToken

		deps.tokens.EXPECT().
			Create(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, created *domain.RefreshToken) (*domain.RefreshToken, error) {
				successor = created

				return created, nil
			}).
			Once()

		pair, err := service.Refresh(context.Background(), value.Reveal())
		if err != nil {
			t.Fatalf("Refresh() error = %v, want nil", err)
		}

		if successor.FamilyID() != token.FamilyID() {
			t.Error("the successor left the family, so a reuse would no longer be detectable")
		}

		// The chain has a fixed end. A successor that pushed it out would let a
		// token refreshed often enough outlive every policy meant to bound it.
		if !successor.ExpiresAt().Equal(token.ExpiresAt()) {
			t.Errorf("successor expires at %s, want the chain's original %s",
				successor.ExpiresAt(), token.ExpiresAt())
		}

		if pair.RefreshToken.Reveal() == value.Reveal() {
			t.Error("Refresh() handed back the token it was given, so nothing rotated")
		}
	})

	t.Run("a token presented twice ends the whole family", func(t *testing.T) {
		deps, service := setupSession(t)
		token, value := issueToken(t)

		// Already spent: the only way this happens is that a second holder
		// exists, and neither of them gets to keep the session.
		token.Revoke(time.Now())

		deps.tokens.EXPECT().FindByHash(mock.Anything, mock.Anything).Return(token, nil).Once()
		deps.tokens.EXPECT().RevokeFamily(mock.Anything, token.FamilyID(), mock.Anything).Return(nil).Once()

		_, err := service.Refresh(context.Background(), value.Reveal())
		assertRefreshRejected(t, err)
	})

	t.Run("losing the race to spend also ends the family", func(t *testing.T) {
		// Spend answering false means another caller got there between the read
		// and the write — the same proof of a second holder, and the case a
		// read-then-write would have missed entirely.
		deps, service := setupSession(t)
		token, value := issueToken(t)

		deps.tokens.EXPECT().FindByHash(mock.Anything, mock.Anything).Return(token, nil).Once()
		deps.users.EXPECT().FindByID(mock.Anything, mock.Anything).Return(newTestUser(t), nil).Once()
		expectIssuedAccessToken(deps, time.Now().Add(15*time.Minute))
		expectInlineTx(deps)
		deps.tokens.EXPECT().Spend(mock.Anything, mock.Anything, mock.Anything).Return(false, nil).Once()

		// Revoked outside the rolled-back transaction. Inside it, detecting the
		// theft and forgetting it would be the same thing.
		deps.tokens.EXPECT().RevokeFamily(mock.Anything, token.FamilyID(), mock.Anything).Return(nil).Once()

		_, err := service.Refresh(context.Background(), value.Reveal())
		assertRefreshRejected(t, err)
	})

	t.Run("an expired chain is rejected without revoking anything", func(t *testing.T) {
		// Nothing was stolen — the chain reached the end it was given. Revoking
		// here would be a write on every abandoned session.
		deps, service := setupSession(t)

		token, value, err := domain.IssueRefreshToken(domain.NewUserID(), time.Nanosecond, time.Now())
		if err != nil {
			t.Fatalf("IssueRefreshToken() error = %v", err)
		}

		deps.tokens.EXPECT().FindByHash(mock.Anything, mock.Anything).Return(token, nil).Once()

		_, err = service.Refresh(context.Background(), value.Reveal())
		assertRefreshRejected(t, err)
	})

	t.Run("a token nobody issued reads the same as a revoked one", func(t *testing.T) {
		deps, service := setupSession(t)

		deps.tokens.EXPECT().
			FindByHash(mock.Anything, mock.Anything).
			Return(nil, errorx.New(errorx.KindNotFound, "refresh token not found")).
			Once()

		_, err := service.Refresh(context.Background(), "never-issued")
		assertRefreshRejected(t, err)
	})

	t.Run("a failed write stays internal", func(t *testing.T) {
		deps, service := setupSession(t)
		token, value := issueToken(t)

		deps.tokens.EXPECT().FindByHash(mock.Anything, mock.Anything).Return(token, nil).Once()
		deps.users.EXPECT().FindByID(mock.Anything, mock.Anything).Return(newTestUser(t), nil).Once()
		expectIssuedAccessToken(deps, time.Now().Add(15*time.Minute))
		expectInlineTx(deps)
		deps.tokens.EXPECT().
			Spend(mock.Anything, mock.Anything, mock.Anything).
			Return(false, errorx.New(errorx.KindInternal, "connection refused")).
			Once()

		_, err := service.Refresh(context.Background(), value.Reveal())
		if got := errorx.KindOf(err); got != errorx.KindInternal {
			t.Errorf("KindOf() = %q, want %q", got, errorx.KindInternal)
		}
	})
}

// assertRefreshRejected checks the single answer every unusable chain gets. The
// reason code is deliberately the same for expired and revoked: telling a caller
// their token was already used tells a thief their copy was the real one.
func assertRefreshRejected(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("Refresh() error = nil, want an error")
	}

	if got := errorx.KindOf(err); got != errorx.KindUnauthenticated {
		t.Errorf("KindOf() = %q, want %q", got, errorx.KindUnauthenticated)
	}

	if got := errorx.Reason(err); got != "REFRESH_TOKEN_INVALID" {
		t.Errorf("Reason() = %q, want REFRESH_TOKEN_INVALID", got)
	}
}

func TestLogout(t *testing.T) {
	t.Run("revokes the family, not just the token presented", func(t *testing.T) {
		// Revoking one link would end nothing: any other link still refreshes.
		deps, service := setupSession(t)
		token, value := issueToken(t)

		deps.tokens.EXPECT().FindByHash(mock.Anything, mock.Anything).Return(token, nil).Once()
		deps.tokens.EXPECT().RevokeFamily(mock.Anything, token.FamilyID(), mock.Anything).Return(nil).Once()

		if err := service.Logout(context.Background(), value.Reveal()); err != nil {
			t.Fatalf("Logout() error = %v, want nil", err)
		}
	})

	t.Run("succeeds for a token that was never issued", func(t *testing.T) {
		// A client retrying after a timeout must not see a failure, and an error
		// would tell a prober which tokens exist.
		deps, service := setupSession(t)

		deps.tokens.EXPECT().
			FindByHash(mock.Anything, mock.Anything).
			Return(nil, errorx.New(errorx.KindNotFound, "refresh token not found")).
			Once()

		if err := service.Logout(context.Background(), "never-issued"); err != nil {
			t.Fatalf("Logout() error = %v, want nil", err)
		}
	})

	t.Run("a broken store is still reported", func(t *testing.T) {
		deps, service := setupSession(t)

		wantErr := errorx.New(errorx.KindInternal, "connection refused")
		deps.tokens.EXPECT().FindByHash(mock.Anything, mock.Anything).Return(nil, wantErr).Once()

		if err := service.Logout(context.Background(), "any"); !errors.Is(err, wantErr) {
			t.Fatalf("Logout() error = %v, want the store's failure", err)
		}
	})
}
