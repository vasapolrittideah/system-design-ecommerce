package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/adapter/out/postgres/sqlc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/port/out"
)

// IdempotencyStore implements the driven port over pgx.
type IdempotencyStore struct {
	pool *pgxpool.Pool
}

var _ out.IdempotencyStore = (*IdempotencyStore)(nil)

// NewIdempotencyStore builds the store over a pool.
func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore {
	return &IdempotencyStore{pool: pool}
}

// Find returns what a key was already used for.
func (s *IdempotencyStore) Find(
	ctx context.Context,
	userID domain.UserID,
	key string,
) (out.Claim, bool, error) {
	id, err := parseID(userID.String(), "user id")
	if err != nil {
		return out.Claim{}, false, err
	}

	row, err := queriesFrom(ctx, s.pool).GetIdempotencyKey(ctx, sqlc.GetIdempotencyKeyParams{
		UserID: id,
		Key:    key,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out.Claim{}, false, nil
		}

		return out.Claim{}, false, errorx.Wrap(err, errorx.KindInternal, "read idempotency key")
	}

	return claimOf(&row), true, nil
}

// Claim takes the key for paymentID, reporting whether this call took it.
//
// The INSERT is what does the deciding, and it has to run in the transaction
// that writes the attempt: a concurrent submit blocks on the primary key until
// that transaction ends, and then either reads the committed claim or takes the
// key its rollback released. That waiting is why there is no in-flight state to
// observe — Postgres holds the second caller rather than this table describing
// it.
func (s *IdempotencyStore) Claim(
	ctx context.Context,
	userID domain.UserID,
	key string,
	requestHash []byte,
	paymentID domain.PaymentID,
) (out.Claim, bool, error) {
	user, err := parseID(userID.String(), "user id")
	if err != nil {
		return out.Claim{}, false, err
	}

	payment, err := parseID(paymentID.String(), "payment id")
	if err != nil {
		return out.Claim{}, false, err
	}

	queries := queriesFrom(ctx, s.pool)

	row, err := queries.ClaimIdempotencyKey(ctx, sqlc.ClaimIdempotencyKeyParams{
		UserID:      user,
		Key:         key,
		RequestHash: requestHash,
		PaymentID:   payment,
	})
	if err == nil {
		return claimOf(&row), true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out.Claim{}, false, errorx.Wrap(err, errorx.KindInternal, "claim idempotency key")
	}

	// ON CONFLICT DO NOTHING returned no row, so somebody else holds the key.
	// Reading it back is what lets the caller tell a retry from a key reused
	// for a different order.
	existing, err := queries.GetIdempotencyKey(ctx, sqlc.GetIdempotencyKeyParams{UserID: user, Key: key})
	if err != nil {
		return out.Claim{}, false, errorx.Wrap(err, errorx.KindInternal, "read the claim that won")
	}

	return claimOf(&existing), false, nil
}

func claimOf(row *sqlc.IdempotencyKey) out.Claim {
	return out.Claim{
		RequestHash: row.RequestHash,
		PaymentID:   domain.PaymentID(row.PaymentID.String()),
	}
}
