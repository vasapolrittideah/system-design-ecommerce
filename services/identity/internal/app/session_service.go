package app

import (
	"context"
	"errors"
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/out"
)

// errInvalidCredentials is the single answer Login gives to a wrong password, an
// address that was never registered, and an address that does not parse. Any
// difference between them is a way to ask which addresses have accounts.
//
// It is built here rather than at the handler because reclassifying is a
// decision about *which* failures are the caller's: a handler that turned
// everything Login returned into a 401 would answer an unreachable database by
// telling every client to sign in again, and the outage would never reach the
// error rate.
//
// The domain sentinel stays underneath, so a log line still records what
// actually happened while the client gets the answer that reveals nothing.
var errInvalidCredentials = errorx.
	Wrap(domain.ErrInvalidCredentials, errorx.KindUnauthenticated, "login").
	WithReason("INVALID_CREDENTIALS")

// SessionConfig is the policy this service applies to the sessions it opens.
type SessionConfig struct {
	// RefreshTTL is how long a rotation chain lives. It bounds the whole chain
	// rather than each link, so it is also the longest a session can survive
	// without the user proving who they are again.
	//
	// 30 days by default: long enough that a phone app does not ask weekly,
	// short enough that an abandoned session is not indefinite. The access token
	// TTL beside it (15m) is the window on a *stolen* credential; this is the
	// window on an unused one.
	RefreshTTL time.Duration `env:"REFRESH_TTL" envDefault:"720h"`
}

// SessionService implements the session use cases.
type SessionService struct {
	cfg    SessionConfig
	users  out.UserRepository
	tokens out.RefreshTokenRepository
	hasher out.PasswordHasher
	issuer out.AccessTokenIssuer
	tx     out.TxManager
}

var _ in.SessionUseCase = (*SessionService)(nil)

// NewSessionService wires the use cases to their driven ports.
func NewSessionService(
	cfg SessionConfig,
	users out.UserRepository,
	tokens out.RefreshTokenRepository,
	hasher out.PasswordHasher,
	issuer out.AccessTokenIssuer,
	tx out.TxManager,
) *SessionService {
	return &SessionService{
		cfg:    cfg,
		users:  users,
		tokens: tokens,
		hasher: hasher,
		issuer: issuer,
		tx:     tx,
	}
}

// Login verifies credentials and opens a session.
//
// Every path through the failure cases costs one password verification,
// including the one where no such user exists. Returning early there would make
// the response time answer a question the error deliberately does not.
func (s *SessionService) Login(ctx context.Context, cmd in.LoginCommand) (in.Session, error) {
	// A malformed address is answered as invalid credentials rather than as a
	// bad request. It is one: nothing that fails to parse can match a stored
	// email, and the shape of an address is already declared in the proto, so
	// reaching here means a client that skipped validation learning nothing
	// extra for it.
	email, err := domain.NewEmail(cmd.Email)
	if err != nil {
		return in.Session{}, errInvalidCredentials
	}

	user, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		if errorx.KindOf(err) != errorx.KindNotFound {
			return in.Session{}, err
		}

		// Verify against a hash that cannot be read, which the hasher spends the
		// same time on as a real one. Its answer is discarded; the time is the
		// whole point.
		_, _ = s.hasher.Verify("", cmd.Password)

		return in.Session{}, errInvalidCredentials
	}

	matches, err := s.hasher.Verify(user.PasswordHash(), cmd.Password)
	if err != nil {
		return in.Session{}, errorx.Wrap(err, errorx.KindInternal, "verify password")
	}

	if !matches {
		return in.Session{}, errInvalidCredentials
	}

	pair, err := s.issue(ctx, user)
	if err != nil {
		return in.Session{}, err
	}

	return in.Session{Tokens: pair, User: user}, nil
}

// Refresh spends the presented token and returns its successor's pair.
func (s *SessionService) Refresh(ctx context.Context, refreshToken string) (in.TokenPair, error) {
	pair, err := s.refresh(ctx, refreshToken)
	if err != nil {
		return in.TokenPair{}, asRefreshFailure(err)
	}

	return pair, nil
}

// refresh is Refresh before its failures are collapsed, so the logic below can
// keep telling an expired chain from a stolen one — which it has to, because
// only one of them ends the family.
func (s *SessionService) refresh(ctx context.Context, refreshToken string) (in.TokenPair, error) {
	now := time.Now()

	token, err := s.tokens.FindByHash(ctx, domain.HashRefreshToken(refreshToken))
	if err != nil {
		if errorx.KindOf(err) == errorx.KindNotFound {
			// A token nobody issued and a token that was revoked get the same
			// answer, because the alternative lets a holder of one string find
			// out whether it was ever real.
			return in.TokenPair{}, domain.ErrRefreshTokenRevoked
		}

		return in.TokenPair{}, err
	}

	if err := token.EnsureUsable(now); err != nil {
		return in.TokenPair{}, s.endChainOnReuse(ctx, token, now, err)
	}

	// Loaded for its roles, which are copied into the new access token. Reading
	// them fresh on every refresh is what makes a role taken away take effect
	// within one access-token TTL rather than at the end of the chain.
	user, err := s.users.FindByID(ctx, token.UserID())
	if err != nil {
		return in.TokenPair{}, err
	}

	// Rotate asks the aggregate the same question EnsureUsable just answered, at
	// the same instant, so this error is unreachable. Checking early is what
	// keeps a replayed token from costing a user lookup first; the aggregate
	// re-checking is not this function's to skip.
	successor, value, err := token.Rotate(now)
	if err != nil {
		return in.TokenPair{}, err
	}

	access, err := s.issuer.Issue(user.ID(), user.Roles())
	if err != nil {
		return in.TokenPair{}, errorx.Wrap(err, errorx.KindInternal, "sign access token")
	}

	// Spending the old token and storing the new one are one write. A crash
	// between them either way is a session the user cannot continue and cannot
	// see the reason for.
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		spent, err := s.tokens.Spend(ctx, token.ID(), now)
		if err != nil {
			return err
		}

		// Zero rows means another caller spent it between the read above and
		// here — the same proof of a second holder as an already-revoked token,
		// and rolling back is what keeps the successor from being stored for a
		// chain that is about to end.
		if !spent {
			return domain.ErrRefreshTokenRevoked
		}

		_, err = s.tokens.Create(ctx, successor)

		return err
	})
	if err != nil {
		if errors.Is(err, domain.ErrRefreshTokenRevoked) {
			return in.TokenPair{}, s.endChainOnReuse(ctx, token, now, err)
		}

		return in.TokenPair{}, err
	}

	return in.TokenPair{
		AccessToken:           access.Value,
		AccessTokenExpiresAt:  access.ExpiresAt,
		RefreshToken:          value,
		RefreshTokenExpiresAt: successor.ExpiresAt(),
	}, nil
}

// Logout revokes the presented token's chain.
func (s *SessionService) Logout(ctx context.Context, refreshToken string) error {
	token, err := s.tokens.FindByHash(ctx, domain.HashRefreshToken(refreshToken))
	if err != nil {
		if errorx.KindOf(err) == errorx.KindNotFound {
			// Nothing to revoke is the state the caller asked for. Reporting it
			// would make a failed logout indistinguishable from a token that
			// never existed, which is a question worth answering for nobody.
			return nil
		}

		return err
	}

	// The family rather than the one token: a logout that left the rest of the
	// chain alive would end nothing, since any other link still refreshes.
	return s.tokens.RevokeFamily(ctx, token.FamilyID(), time.Now())
}

// issue mints a pair for user and stores the refresh half.
//
// Signing comes first so that a storage failure leaves nothing behind. The other
// order would write a row for a pair that was never returned, and those rows are
// indistinguishable from live sessions until they expire.
func (s *SessionService) issue(ctx context.Context, user *domain.User) (in.TokenPair, error) {
	token, value, err := domain.IssueRefreshToken(user.ID(), s.cfg.RefreshTTL, time.Now())
	if err != nil {
		return in.TokenPair{}, errorx.Wrap(err, errorx.KindInternal, "issue refresh token")
	}

	access, err := s.issuer.Issue(user.ID(), user.Roles())
	if err != nil {
		return in.TokenPair{}, errorx.Wrap(err, errorx.KindInternal, "sign access token")
	}

	stored, err := s.tokens.Create(ctx, token)
	if err != nil {
		return in.TokenPair{}, err
	}

	return in.TokenPair{
		AccessToken:           access.Value,
		AccessTokenExpiresAt:  access.ExpiresAt,
		RefreshToken:          value,
		RefreshTokenExpiresAt: stored.ExpiresAt(),
	}, nil
}

// asRefreshFailure collapses the reasons a chain could not be used into the one
// answer the wire may carry, and leaves everything else — a failed write, a
// cancelled context — exactly as it was, so a broken service still reports
// itself as broken.
//
// Expired and revoked are not split apart here even though the domain
// distinguishes them and this service acts on the difference. Telling a caller
// their token was already used tells a thief their copy was the real one, and
// the client's next move is the same either way: sign in again.
func asRefreshFailure(err error) error {
	if !errors.Is(err, domain.ErrRefreshTokenExpired) && !errors.Is(err, domain.ErrRefreshTokenRevoked) {
		return err
	}

	return errorx.Wrap(err, errorx.KindUnauthenticated, "refresh").
		WithReason("REFRESH_TOKEN_INVALID")
}

// endChainOnReuse revokes the token's family when cause says a second holder
// exists, and returns cause either way.
//
// It runs outside any transaction on purpose. The revocation is a durable
// consequence of the error being returned, so putting it inside the write that
// is about to roll back would detect the theft and then forget it.
func (s *SessionService) endChainOnReuse(
	ctx context.Context,
	token *domain.RefreshToken,
	now time.Time,
	cause error,
) error {
	// An expired chain reached its own end and proves nothing about who holds
	// it. Only a token that was still supposed to be spendable, and was not, is
	// evidence.
	if !errors.Is(cause, domain.ErrRefreshTokenRevoked) {
		return cause
	}

	if err := s.tokens.RevokeFamily(ctx, token.FamilyID(), now); err != nil {
		return err
	}

	return cause
}
