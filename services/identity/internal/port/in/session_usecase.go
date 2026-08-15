package in

import (
	"context"
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
)

// LoginCommand is a request to exchange credentials for a token pair.
type LoginCommand struct {
	Email string

	// Password is plaintext, and is why this struct is never logged or attached
	// to a span. It lives for the duration of one verification.
	Password string
}

// TokenPair is what a sign-in or a refresh produces.
//
// Both token values are [domain.TokenValue], so neither half can reach a log
// line by being printed: an access token is a working credential until it
// expires, and a refresh token is one until someone revokes it.
type TokenPair struct {
	AccessToken          domain.TokenValue
	AccessTokenExpiresAt time.Time

	RefreshToken          domain.TokenValue
	RefreshTokenExpiresAt time.Time
}

// Session is a completed sign-in: the pair, plus the user it was minted for.
//
// The user rides along because the screen that just signed them in would
// otherwise ask for what this call already loaded. A refresh returns no user for
// the opposite reason — nothing on that screen has changed.
type Session struct {
	Tokens TokenPair
	User   *domain.User
}

// SessionUseCase is everything that can be done to a session.
//
// Separate from [UserUseCase] rather than three more methods on it, because the
// reasons to change have genuinely diverged: this slice depends on a signer, a
// token store, and a transaction, and none of those belong to reading a user.
type SessionUseCase interface {
	// Login verifies credentials and opens a session.
	//
	// A wrong password and an address with no account are the same answer, and
	// cost the same time to produce. Any difference between them — in the error,
	// in the reason code, or in how long the call took — turns this into a way
	// to ask which addresses are registered.
	Login(ctx context.Context, cmd LoginCommand) (Session, error)

	// Refresh spends the presented token and returns a new pair.
	//
	// Presenting a token that cannot be spent revokes its whole chain, because
	// the only way it happens is that a second holder exists. That makes this
	// unsafe to retry automatically: a retry presents a token the first attempt
	// already spent, which is indistinguishable from the theft it detects.
	Refresh(ctx context.Context, refreshToken string) (TokenPair, error)

	// Logout revokes the presented token's whole chain.
	//
	// It succeeds for a token that is already revoked, expired, or was never
	// issued. Logging out is a state the caller wants to reach rather than a
	// change they are making, and an error would tell a prober which tokens
	// exist.
	//
	// The access token already in the caller's hands keeps working until it
	// expires. Nothing here can change that.
	Logout(ctx context.Context, refreshToken string) error
}
