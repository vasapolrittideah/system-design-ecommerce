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

	// Update writes an order that has moved and the events it raised, carrying
	// the version it was loaded at.
	//
	// A stale version affects zero rows and comes back as a conflict rather
	// than silently overwriting: two writers deciding what becomes of one
	// order is the ordinary case here, and the loser has to find out that it
	// lost.
	Update(ctx context.Context, order *domain.Order) (*domain.Order, error)

	// FindByID returns one order, or ErrOrderNotFound.
	FindByID(ctx context.Context, id domain.OrderID) (*domain.Order, error)

	// FindByIDForUpdate is FindByID holding the row until the surrounding
	// transaction ends, and it is only meaningful inside one.
	//
	// Advancing an order reads it and then writes it. Locking the row makes
	// Postgres order the writers that meet — a payment's outcome and a saga
	// timeout both decide what becomes of one order — so the second reads the
	// first one's outcome rather than racing it to a version number.
	FindByIDForUpdate(ctx context.Context, id domain.OrderID) (*domain.Order, error)

	// ClaimStaleOrders returns at most limit orders still waiting for payment
	// that were created no later than createdBefore, holding each row until the
	// surrounding transaction ends.
	//
	// SKIP LOCKED rather than a plain read, so that two workers — a rolling
	// restart is the ordinary case — divide the backlog instead of one waiting
	// behind the other. Returning fewer than limit is how a caller learns it
	// has caught up.
	ClaimStaleOrders(ctx context.Context, createdBefore time.Time, limit int) ([]*domain.Order, error)

	// List pages through one customer's orders, newest first.
	List(ctx context.Context, filter OrderFilter) ([]*domain.Order, error)
}
