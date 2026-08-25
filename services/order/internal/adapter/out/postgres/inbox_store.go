package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/inbox"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out"
)

// InboxStore implements the driven port over pkg/inbox. There is no sqlc
// query behind it: the claim is one statement pkg/inbox already owns, so this
// adapter only binds it to this service's pool.
type InboxStore struct {
	pool *pgxpool.Pool
}

var _ out.InboxStore = (*InboxStore)(nil)

// NewInboxStore builds the store over a pool.
func NewInboxStore(pool *pgxpool.Pool) *InboxStore {
	return &InboxStore{pool: pool}
}

// Claim takes eventID for consumerGroup.
func (s *InboxStore) Claim(ctx context.Context, consumerGroup, eventID string) (bool, error) {
	claimed, err := inbox.Claim(ctx, txmanager.From(ctx, s.pool), consumerGroup, eventID)
	if err != nil {
		return false, errorx.Wrap(err, errorx.KindInternal, "claim event")
	}

	return claimed, nil
}
