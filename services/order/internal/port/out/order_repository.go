package out

import (
	"context"
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
)

// OrderCursor is where a page of orders ended.
//
// Two fields rather than one, because two orders written in the same
// millisecond share a timestamp: paging on it alone would drop or repeat
// whichever of them straddled the boundary.
type OrderCursor struct {
	CreatedAt time.Time
	ID        domain.OrderID
}

// OrderFilter is one page of one customer's orders.
type OrderFilter struct {
	// UserID is whose orders these are. There is no listing across customers:
	// the only screen this serves is somebody's own order history.
	UserID domain.UserID

	// After is nil for the first page.
	After *OrderCursor

	// Limit is how many rows to return, and the caller asks for one more than
	// it means to show — a full page is how it learns another exists, without a
	// second query counting rows nobody will read.
	Limit int
}

// OrderRepository stores orders.
type OrderRepository interface {
	// Create writes the order, its lines, and the events it raised — all
	// through whatever transaction the context carries. Draining the events
	// here rather than in the use case is what keeps the announcement inside
	// the same commit as the order.
	Create(ctx context.Context, order *domain.Order) (*domain.Order, error)

	// FindByID returns one order, or ErrOrderNotFound.
	FindByID(ctx context.Context, id domain.OrderID) (*domain.Order, error)

	// List pages through one customer's orders, newest first.
	List(ctx context.Context, filter OrderFilter) ([]*domain.Order, error)
}
