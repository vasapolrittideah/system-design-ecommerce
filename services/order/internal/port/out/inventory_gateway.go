package out

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
)

// ReservationLine is one SKU and how many of it to hold.
type ReservationLine struct {
	SKU      domain.SKU
	Quantity int
}

// InventoryGateway is what this service may ask of the warehouse.
//
// Reserving is synchronous and happens before the order is persisted, so a
// shopper learns that something is sold out now rather than in an email later.
// Releasing is the compensating step, and both are idempotent at the far end
// because every step of a saga is retried eventually.
type InventoryGateway interface {
	// Reserve holds stock for the order. The order's id is the idempotency key
	// the far end deduplicates on, which is why it is minted before the row
	// exists.
	//
	// Not enough stock comes back classified as a conflict, carrying the SKU
	// that failed, so the caller can name the line rather than the order.
	Reserve(ctx context.Context, orderID domain.OrderID, lines []ReservationLine) (domain.ReservationID, error)

	// Release gives a hold back. It is called to compensate a checkout that
	// could not be finished after the stock was taken.
	Release(ctx context.Context, reservationID domain.ReservationID) error
}
