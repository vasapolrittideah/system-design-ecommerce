package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres/postgrestest"
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/out/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
)

const tokenTTL = 720 * time.Hour

// setupTokens gives each test its own database, plus a stored user for the
// refresh_tokens foreign key to point at.
func setupTokens(t *testing.T) (*pgxpool.Pool, *adapter.RefreshTokenRepository, *domain.User) {
	t.Helper()

	pool := postgrestest.New(t, upMigrations(t)...)

	user, err := adapter.NewUserRepository(pool).Create(context.Background(), newUser(t, "ada@example.com"))
	if err != nil {
		t.Fatalf("Create() user error = %v", err)
	}

	return pool, adapter.NewRefreshTokenRepository(pool), user
}

func issue(t *testing.T, userID domain.UserID) (*domain.RefreshToken, domain.TokenValue) {
	t.Helper()

	token, value, err := domain.IssueRefreshToken(userID, tokenTTL, time.Now())
	if err != nil {
		t.Fatalf("IssueRefreshToken() error = %v", err)
	}

	return token, value
}

func TestCreateAndFindRefreshTokenByHash(t *testing.T) {
	_, tokens, user := setupTokens(t)
	ctx := context.Background()

	token, value := issue(t, user.ID())

	stored, err := tokens.Create(ctx, token)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if stored.CreatedAt().IsZero() {
		t.Error("created_at is zero, want the value the database set")
	}

	// The round trip is the point: a 32-byte column becoming a TokenHash again
	// is what makes the presented token findable at all.
	found, err := tokens.FindByHash(ctx, domain.HashRefreshToken(value.Reveal()))
	if err != nil {
		t.Fatalf("FindByHash() error = %v, want nil", err)
	}

	if found.ID() != token.ID() || found.FamilyID() != token.FamilyID() {
		t.Errorf("FindByHash() = %s/%s, want %s/%s",
			found.ID(), found.FamilyID(), token.ID(), token.FamilyID())
	}

	if found.IsRevoked() {
		t.Error("a freshly stored token reads as revoked, want live")
	}
}

func TestFindRefreshTokenByHashReportsNotFound(t *testing.T) {
	_, tokens, _ := setupTokens(t)

	_, err := tokens.FindByHash(context.Background(), domain.HashRefreshToken("never issued"))
	if got := errorx.KindOf(err); got != errorx.KindNotFound {
		t.Errorf("KindOf() = %q, want %q", got, errorx.KindNotFound)
	}
}

func TestSpendRefreshTokenSucceedsOnce(t *testing.T) {
	// The whole concurrency guard. Two refreshes holding the same token must
	// produce one success and one rejection, and the WHERE clause is what
	// decides — a read followed by a write would let both through.
	_, tokens, user := setupTokens(t)
	ctx := context.Background()

	token, value := issue(t, user.ID())
	if _, err := tokens.Create(ctx, token); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	now := time.Now()

	spent, err := tokens.Spend(ctx, token.ID(), now)
	if err != nil {
		t.Fatalf("Spend() error = %v, want nil", err)
	}

	if !spent {
		t.Fatal("Spend() = false on a live token, want true")
	}

	again, err := tokens.Spend(ctx, token.ID(), now)
	if err != nil {
		t.Fatalf("Spend() second call error = %v, want nil", err)
	}

	if again {
		t.Error("Spend() = true on an already-spent token, want false")
	}

	found, err := tokens.FindByHash(ctx, domain.HashRefreshToken(value.Reveal()))
	if err != nil {
		t.Fatalf("FindByHash() error = %v", err)
	}

	if !found.IsRevoked() {
		t.Error("the spent token still reads as live")
	}
}

func TestRevokeFamilyEndsEveryLiveLink(t *testing.T) {
	_, tokens, user := setupTokens(t)
	ctx := context.Background()

	head, headValue := issue(t, user.ID())
	if _, err := tokens.Create(ctx, head); err != nil {
		t.Fatalf("Create() head error = %v", err)
	}

	successor, successorValue, err := head.Rotate(time.Now())
	if err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}

	if _, err := tokens.Create(ctx, successor); err != nil {
		t.Fatalf("Create() successor error = %v", err)
	}

	if err := tokens.RevokeFamily(ctx, head.FamilyID(), time.Now()); err != nil {
		t.Fatalf("RevokeFamily() error = %v, want nil", err)
	}

	// Every link, not just the one presented: leaving one alive would end
	// nothing, because that one still refreshes.
	for _, value := range []domain.TokenValue{headValue, successorValue} {
		found, err := tokens.FindByHash(ctx, domain.HashRefreshToken(value.Reveal()))
		if err != nil {
			t.Fatalf("FindByHash() error = %v", err)
		}

		if !found.IsRevoked() {
			t.Errorf("token %s survived the family revocation", found.ID())
		}
	}

	// Idempotent, because a client retrying a logout after a timeout must not
	// see a failure.
	if err := tokens.RevokeFamily(ctx, head.FamilyID(), time.Now()); err != nil {
		t.Errorf("RevokeFamily() second call error = %v, want nil", err)
	}
}

func TestRevokeFamilyKeepsTheFirstTimestamp(t *testing.T) {
	// The moment a token stopped working is the interesting one. A second
	// revocation rewriting it would make an audit read as though the session
	// ended later than it did.
	_, tokens, user := setupTokens(t)
	ctx := context.Background()

	token, value := issue(t, user.ID())
	if _, err := tokens.Create(ctx, token); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	spentAt := time.Now()
	if _, err := tokens.Spend(ctx, token.ID(), spentAt); err != nil {
		t.Fatalf("Spend() error = %v", err)
	}

	if err := tokens.RevokeFamily(ctx, token.FamilyID(), spentAt.Add(time.Hour)); err != nil {
		t.Fatalf("RevokeFamily() error = %v", err)
	}

	found, err := tokens.FindByHash(ctx, domain.HashRefreshToken(value.Reveal()))
	if err != nil {
		t.Fatalf("FindByHash() error = %v", err)
	}

	if got := found.RevokedAt(); !got.Truncate(time.Millisecond).Equal(spentAt.UTC().Truncate(time.Millisecond)) {
		t.Errorf("revoked_at = %s, want the first revocation at %s", got, spentAt.UTC())
	}
}
