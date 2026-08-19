// Package in declares the driving ports: what can be asked of this service,
// stated without reference to how the asking arrives. A gRPC handler maps its
// request into one of the commands below and calls the interface; so does the
// reaper, whose caller is a ticker rather than a request.
package in

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
)

// CreateStockItemCommand starts tracking a SKU.
//
// The fields are the raw values that arrived, not domain types: uppercasing a
// SKU and refusing a negative count are rules, and rules are not the adapter's
// to apply.
type CreateStockItemCommand struct {
	SKU       string
	Available int32
}

// AdjustStockCommand moves a count — a delivery arriving, a breakage written
// off.
type AdjustStockCommand struct {
	SKU string

	// Delta is signed and must not be zero. An adjustment that would take the
	// count below zero is refused rather than clamped.
	Delta int32

	// Reason is for the humans reading the log line. Nothing branches on it.
	Reason string
}

// NewLine is one SKU a caller wants held.
type NewLine struct {
	SKU      string
	Quantity int32
}

// ReserveStockCommand is a request to hold stock for an order.
type ReserveStockCommand struct {
	// OrderID is the order taking the hold, and the idempotency key.
	OrderID string

	// Lines may name a SKU twice; the quantities are summed. How long the hold
	// lasts is deliberately not here — it is this service's policy, not the
	// caller's, and a caller that could choose would choose forever.
	Lines []NewLine
}

// InventoryUseCase is everything that can be asked about or done to the
// warehouse over gRPC.
//
// One interface rather than one per use case, and reads alongside writes,
// because they share a lifetime and a dependency set — two repositories and a
// transaction.
type InventoryUseCase interface {
	// GetStockBySKUs reads the counts for many SKUs, returning only the ones
	// this service tracks. This is the read a caller fans out to fill a screen,
	// so one untracked SKU must not fail the screen.
	GetStockBySKUs(ctx context.Context, skus []string) ([]*domain.StockItem, error)

	// GetReservation reads one hold, and reports not found rather than nil.
	GetReservation(ctx context.Context, id string) (*domain.Reservation, error)

	// CreateStockItem starts tracking a SKU at a starting count.
	CreateStockItem(ctx context.Context, cmd CreateStockItemCommand) (*domain.StockItem, error)

	// AdjustStock moves the available count by a delta.
	AdjustStock(ctx context.Context, cmd AdjustStockCommand) (*domain.StockItem, error)

	// ReserveStock holds stock for an order, or returns the hold that order
	// already has. Not enough stock is a conflict naming the SKU.
	ReserveStock(ctx context.Context, cmd ReserveStockCommand) (*domain.Reservation, error)

	// CommitReservation turns a hold into a sale, and succeeds against one that
	// is already committed.
	CommitReservation(ctx context.Context, id string) (*domain.Reservation, error)

	// ReleaseReservation gives the held stock back, and succeeds against one
	// that is already released or expired.
	ReleaseReservation(ctx context.Context, id string) (*domain.Reservation, error)
}

// ReservationReaper returns the capacity of holds nobody committed in time.
//
// A driving port of its own rather than one more method on InventoryUseCase,
// because its caller is a timer in a separate process: nothing reachable over
// gRPC may sweep the warehouse, and a handler that could would be one accidental
// route away from a client doing it.
type ReservationReaper interface {
	// ExpireReservations sweeps at most limit expired holds and reports how many
	// it moved. Returning fewer than limit is how the caller learns it has
	// caught up.
	ExpireReservations(ctx context.Context, limit int) (int, error)
}
