// Package out declares the driven ports: what the inventory service needs from
// the world outside it, in the vocabulary of the use cases that call them rather
// than of the adapters that implement them.
package out

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
)

// StockRepository stores and moves the counts.
//
// Every method takes a context because the adapter pulls the current transaction
// off it — a use case that wraps several of these in txmanager.Do gets them in
// one transaction without any of these signatures changing.
//
// The four movement methods take a SKU and a quantity rather than an aggregate,
// and that is the port doing what the domain comment describes: each one is a
// single conditional statement the server evaluates against the current row, so
// there is no loaded object to hand back and forth and no window between reading
// a count and acting on it.
type StockRepository interface {
	// Create starts tracking a SKU, and reports a conflict when one is already
	// tracked — the UNIQUE constraint is the guard, since checking then
	// inserting leaves a window where two requests both find nothing.
	Create(ctx context.Context, item *domain.StockItem) (*domain.StockItem, error)

	// FindBySKUs returns the counts that exist, in no guaranteed order and
	// possibly fewer than were asked for. A SKU nobody tracks is not an error.
	FindBySKUs(ctx context.Context, skus []domain.SKU) ([]*domain.StockItem, error)

	// Adjust moves the available count and returns the row as it now stands. An
	// adjustment that would take it below zero is a conflict, and an untracked
	// SKU is not found.
	Adjust(ctx context.Context, sku domain.SKU, delta domain.Adjustment) (*domain.StockItem, error)

	// Reserve moves quantity from available to reserved.
	//
	// It fails with domain.ErrInsufficientStock when there is not enough, and
	// that answer is the database's rather than a check this repository ran
	// first: the statement carries the comparison, so two callers racing for the
	// last unit produce one success and one refusal.
	Reserve(ctx context.Context, sku domain.SKU, quantity domain.Quantity) error

	// Release moves quantity back from reserved to available, for a hold that
	// was given up or swept.
	Release(ctx context.Context, sku domain.SKU, quantity domain.Quantity) error

	// Commit removes quantity from reserved without returning it. This is the
	// one movement that reduces what the warehouse holds: the goods left.
	Commit(ctx context.Context, sku domain.SKU, quantity domain.Quantity) error
}
