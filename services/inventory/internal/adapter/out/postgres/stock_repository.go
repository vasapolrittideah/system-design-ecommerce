package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/adapter/out/postgres/sqlc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/port/out"
)

// StockRepository implements the driven port over pgx.
type StockRepository struct {
	pool *pgxpool.Pool
}

var _ out.StockRepository = (*StockRepository)(nil)

// NewStockRepository builds the repository over a pool.
func NewStockRepository(pool *pgxpool.Pool) *StockRepository {
	return &StockRepository{pool: pool}
}

// queries binds sqlc to whichever handle is correct for this call: the
// transaction txmanager put on the context, or the pool when there is none. It
// is what lets the same method run inside a use case's tx.Do and outside one
// without either saying so.
func (r *StockRepository) queries(ctx context.Context) *sqlc.Queries {
	return sqlc.New(txmanager.From(ctx, r.pool))
}

// Create starts tracking a SKU.
func (r *StockRepository) Create(ctx context.Context, item *domain.StockItem) (*domain.StockItem, error) {
	snapshot := item.Snapshot()

	id, err := parseUUID("stock item id", snapshot.ID.String())
	if err != nil {
		return nil, err
	}

	row, err := r.queries(ctx).CreateStockItem(ctx, sqlc.CreateStockItemParams{
		ID:        id,
		Sku:       snapshot.SKU.String(),
		Available: snapshot.Available.Int32(),
	})
	if err != nil {
		if isConstraintViolation(err, skuConstraint) {
			// New rather than Wrap, because only an Internal message is scrubbed
			// by ToGRPC: wrapping would describe the schema to anyone who can
			// reach the API.
			return nil, errorx.New(errorx.KindConflict, "sku is already tracked").
				WithReason("SKU_ALREADY_TRACKED").
				WithMetadata(map[string]string{"sku": snapshot.SKU.String()})
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "create stock item %s", snapshot.SKU)
	}

	return toStockDomain(&row), nil
}

// FindBySKUs returns the counts that exist, and no error for the SKUs that do
// not.
func (r *StockRepository) FindBySKUs(ctx context.Context, skus []domain.SKU) ([]*domain.StockItem, error) {
	if len(skus) == 0 {
		return nil, nil
	}

	values := make([]string, 0, len(skus))
	for _, sku := range skus {
		values = append(values, sku.String())
	}

	rows, err := r.queries(ctx).GetStockItemsBySKUs(ctx, values)
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "get %d stock item(s)", len(skus))
	}

	items := make([]*domain.StockItem, 0, len(rows))
	for i := range rows {
		items = append(items, toStockDomain(&rows[i]))
	}

	return items, nil
}

// Adjust moves the available count.
func (r *StockRepository) Adjust(
	ctx context.Context,
	sku domain.SKU,
	delta domain.Adjustment,
) (*domain.StockItem, error) {
	row, err := r.queries(ctx).AdjustStock(ctx, sqlc.AdjustStockParams{
		Sku:   sku.String(),
		Delta: delta.Int32(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Two different failures produce no rows here, and a caller needs to
			// tell them apart: a SKU nobody tracks is a 404 they should stop
			// asking about, and a delta that would go below zero is a 409 about
			// a count they can look up.
			return nil, r.explainMiss(ctx, sku,
				errorx.New(errorx.KindConflict, "adjusting %s by %d would take it below zero", sku, delta).
					WithReason("STOCK_WOULD_GO_NEGATIVE").
					WithMetadata(map[string]string{"sku": sku.String()}))
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "adjust stock for %s", sku)
	}

	return toStockDomain(&row), nil
}

// Reserve moves quantity from available to reserved.
//
// The whole of the oversell guard is the predicate inside the statement, so
// there is nothing to check before calling it and nothing to hold between a read
// and a write.
func (r *StockRepository) Reserve(ctx context.Context, sku domain.SKU, quantity domain.Quantity) error {
	_, err := r.queries(ctx).ReserveStock(ctx, sqlc.ReserveStockParams{
		Sku:      sku.String(),
		Quantity: quantity.Int32(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Wrapped rather than replaced, so that errors.Is against the domain
			// sentinel still answers for a use case that wants to branch on it,
			// while the client gets the reason code and the SKU it needs to name
			// the line that failed.
			return r.explainMiss(ctx, sku,
				errorx.Wrap(domain.ErrInsufficientStock, errorx.KindConflict, "reserve %d of %s", quantity, sku).
					WithReason("OUT_OF_STOCK").
					WithMetadata(map[string]string{"sku": sku.String()}))
		}

		return errorx.Wrap(err, errorx.KindInternal, "reserve %d of %s", quantity, sku)
	}

	return nil
}

// Release moves quantity back from reserved to available.
func (r *StockRepository) Release(ctx context.Context, sku domain.SKU, quantity domain.Quantity) error {
	_, err := r.queries(ctx).ReleaseStock(ctx, sqlc.ReleaseStockParams{
		Sku:      sku.String(),
		Quantity: quantity.Int32(),
	})

	return r.movedHold(ctx, "release", sku, quantity, err)
}

// Commit removes quantity from reserved without returning it.
func (r *StockRepository) Commit(ctx context.Context, sku domain.SKU, quantity domain.Quantity) error {
	_, err := r.queries(ctx).CommitStock(ctx, sqlc.CommitStockParams{
		Sku:      sku.String(),
		Quantity: quantity.Int32(),
	})

	return r.movedHold(ctx, "commit", sku, quantity, err)
}

// movedHold classifies what releasing or committing a hold can fail with.
//
// Both statements guard on `reserved >= quantity`, and unlike the reserving
// guard that predicate should never refuse: the quantity comes from a
// reservation this service wrote, and the aggregate's state machine has already
// refused to move the same hold twice. Reaching it means the counts and the
// reservations disagree, which is a bug here rather than anything a caller did —
// so it is Internal, and it says so loudly instead of leaving stock stranded in
// reserved with nobody told.
func (r *StockRepository) movedHold(
	ctx context.Context,
	what string,
	sku domain.SKU,
	quantity domain.Quantity,
	err error,
) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return r.explainMiss(ctx, sku,
			errorx.New(errorx.KindInternal, "%s %d of %s: reserved is lower than the hold", what, quantity, sku).
				WithReason("STOCK_INCONSISTENT"))
	}

	return errorx.Wrap(err, errorx.KindInternal, "%s %d of %s", what, quantity, sku)
}

// explainMiss decides which of two failures a zero row count was.
//
// Every movement statement matches nothing both for a SKU that is not tracked
// and for one whose predicate refused, and only a second read can tell them
// apart. It runs on the failure path alone, so the cost lands on the request
// that was already going to fail — and it is not racy in any way that matters,
// since a SKU that appeared between the two reads was still absent when the
// decision was taken.
func (r *StockRepository) explainMiss(ctx context.Context, sku domain.SKU, refused error) error {
	_, err := r.queries(ctx).GetStockItemBySKU(ctx, sku.String())
	if err == nil {
		return refused
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return errorx.Wrap(domain.ErrStockItemNotFound, errorx.KindNotFound, "sku %s is not tracked", sku).
			WithReason("SKU_NOT_TRACKED").
			WithMetadata(map[string]string{"sku": sku.String()})
	}

	return errorx.Wrap(err, errorx.KindInternal, "look up stock item %s", sku)
}

// toStockDomain rebuilds the read model from its row. It reconstitutes rather
// than constructs: re-running today's bounds over yesterday's data is how a
// service loses the ability to read the counts it wrote itself.
func toStockDomain(row *sqlc.StockItem) *domain.StockItem {
	return domain.ReconstituteStockItem(domain.StockItemSnapshot{
		ID:        domain.StockItemID(row.ID.String()),
		SKU:       domain.SKU(row.Sku),
		Available: domain.Quantity(row.Available),
		Reserved:  domain.Quantity(row.Reserved),
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
		Version:   int(row.Version),
	})
}
