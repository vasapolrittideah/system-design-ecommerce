package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/out/postgres/sqlc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/out"
)

// RefreshTokenRepository implements the driven port over pgx.
type RefreshTokenRepository struct {
	pool *pgxpool.Pool
}

var _ out.RefreshTokenRepository = (*RefreshTokenRepository)(nil)

// NewRefreshTokenRepository builds the repository over a pool.
func NewRefreshTokenRepository(pool *pgxpool.Pool) *RefreshTokenRepository {
	return &RefreshTokenRepository{pool: pool}
}

// queries binds sqlc to the transaction on the context, or to the pool when
// there is none. Rotation runs both of its writes inside one tx.Do, and this is
// what makes that work without either method saying so.
func (r *RefreshTokenRepository) queries(ctx context.Context) *sqlc.Queries {
	return sqlc.New(txmanager.From(ctx, r.pool))
}

// Create inserts the token and returns the row as stored.
//
// A duplicate token_hash is left to the generic Internal path rather than being
// mapped to a conflict. It would mean two 256-bit CSPRNG draws collided, which
// is a broken randomness source and not something a caller did.
func (r *RefreshTokenRepository) Create(
	ctx context.Context,
	token *domain.RefreshToken,
) (*domain.RefreshToken, error) {
	snapshot := token.Snapshot()

	id, err := uuid.Parse(snapshot.ID.String())
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "parse refresh token id")
	}

	userID, err := uuid.Parse(snapshot.UserID.String())
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "parse user id")
	}

	familyID, err := uuid.Parse(snapshot.FamilyID.String())
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "parse refresh token family id")
	}

	row, err := r.queries(ctx).CreateRefreshToken(ctx, sqlc.CreateRefreshTokenParams{
		ID:        id,
		UserID:    userID,
		TokenHash: snapshot.Hash[:],
		FamilyID:  familyID,
		ExpiresAt: snapshot.ExpiresAt,
	})
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "create refresh token")
	}

	return toRefreshTokenDomain(&row)
}

// FindByHash returns one token.
func (r *RefreshTokenRepository) FindByHash(
	ctx context.Context,
	hash domain.TokenHash,
) (*domain.RefreshToken, error) {
	row, err := r.queries(ctx).GetRefreshTokenByHash(ctx, hash[:])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Nothing identifying in the message: the value looked up is a
			// bearer credential, and the caller answers this the same way it
			// answers a revoked token anyway.
			return nil, errorx.New(errorx.KindNotFound, "refresh token not found").
				WithReason("REFRESH_TOKEN_NOT_FOUND")
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "get refresh token")
	}

	return toRefreshTokenDomain(&row)
}

// Spend revokes a live token and reports whether this call is what revoked it.
func (r *RefreshTokenRepository) Spend(
	ctx context.Context,
	id domain.RefreshTokenID,
	revokedAt time.Time,
) (bool, error) {
	parsed, err := uuid.Parse(id.String())
	if err != nil {
		return false, errorx.Wrap(err, errorx.KindInternal, "parse refresh token id")
	}

	rows, err := r.queries(ctx).SpendRefreshToken(ctx, sqlc.SpendRefreshTokenParams{
		ID:        parsed,
		RevokedAt: pgtype.Timestamptz{Time: revokedAt, Valid: true},
	})
	if err != nil {
		return false, errorx.Wrap(err, errorx.KindInternal, "spend refresh token")
	}

	// Zero rows is not an error here — it is the answer. The query matched no
	// live token, meaning someone else already spent this one.
	return rows > 0, nil
}

// RevokeFamily ends every live token in the chain.
func (r *RefreshTokenRepository) RevokeFamily(
	ctx context.Context,
	family domain.FamilyID,
	revokedAt time.Time,
) error {
	parsed, err := uuid.Parse(family.String())
	if err != nil {
		return errorx.Wrap(err, errorx.KindInternal, "parse refresh token family id")
	}

	err = r.queries(ctx).RevokeRefreshTokenFamily(ctx, sqlc.RevokeRefreshTokenFamilyParams{
		FamilyID:  parsed,
		RevokedAt: pgtype.Timestamptz{Time: revokedAt, Valid: true},
	})
	if err != nil {
		return errorx.Wrap(err, errorx.KindInternal, "revoke refresh token family")
	}

	return nil
}

// toRefreshTokenDomain rebuilds the aggregate from a row.
//
// The hash is copied into a fixed-size array rather than kept as the row's
// slice, which is where the only error comes from: a column that is not 32 bytes
// is not a SHA-256, and reconstituting one would produce an aggregate that can
// never match anything.
func toRefreshTokenDomain(row *sqlc.RefreshToken) (*domain.RefreshToken, error) {
	if len(row.TokenHash) != len(domain.TokenHash{}) {
		return nil, errorx.New(errorx.KindInternal, "refresh token hash is %d bytes", len(row.TokenHash))
	}

	var hash domain.TokenHash
	copy(hash[:], row.TokenHash)

	return domain.ReconstituteRefreshToken(domain.RefreshTokenSnapshot{
		ID:        domain.RefreshTokenID(row.ID.String()),
		UserID:    domain.UserID(row.UserID.String()),
		Hash:      hash,
		FamilyID:  domain.FamilyID(row.FamilyID.String()),
		ExpiresAt: row.ExpiresAt,
		// A NULL revoked_at is the zero time on the aggregate, which is how it
		// reads "still live". pgtype leaves Time zero when Valid is false, so
		// the two representations already agree.
		RevokedAt: row.RevokedAt.Time,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
		Version:   int(row.Version),
	}), nil
}
