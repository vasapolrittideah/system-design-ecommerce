package out

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
)

// PaymentRepository stores attempts to collect.
type PaymentRepository interface {
	// Create writes a new attempt and any events it raised — all through
	// whatever transaction the context carries. Draining the events here rather
	// than in the use case is what keeps an announcement inside the same commit
	// as the thing it announces.
	Create(ctx context.Context, payment *domain.Payment) (*domain.Payment, error)

	// Update writes a settled attempt and the events it raised, carrying the
	// version it was loaded at.
	//
	// A stale version affects zero rows and comes back as a conflict rather
	// than silently overwriting: a provider callback and a reconciliation
	// arriving together are two writers settling one charge, and the loser has
	// to find out that it lost.
	Update(ctx context.Context, payment *domain.Payment) (*domain.Payment, error)

	// FindByID returns one attempt, or ErrPaymentNotFound.
	FindByID(ctx context.Context, id domain.PaymentID) (*domain.Payment, error)

	// FindByIDForUpdate is FindByID holding the row until the surrounding
	// transaction ends, and it is only meaningful inside one.
	//
	// Settling reads an attempt and then writes it, and the two writers that
	// meet here are ordinary rather than rare: a provider's callback can arrive
	// before the response to the call that caused it. Locking the row makes
	// Postgres do the waiting, so the second writer reads the first one's
	// outcome instead of racing it to a version number and losing.
	FindByIDForUpdate(ctx context.Context, id domain.PaymentID) (*domain.Payment, error)

	// FindByOrderIDs returns every attempt made against any of these orders, in
	// no guaranteed order. An order nobody has tried to pay for contributes
	// nothing and is not an error.
	FindByOrderIDs(ctx context.Context, orderIDs []domain.OrderID) ([]*domain.Payment, error)
}
