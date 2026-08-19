package out

import (
	"context"
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
)

// ReservationRepository stores and reads reservation aggregates.
//
// The aggregate is always whole: a reservation arrives with its lines and is
// written with them, because what has to be given back when it is released is
// exactly the set of lines it was taken with.
type ReservationRepository interface {
	// Create persists a hold that has never been stored, with its lines, and
	// returns it as the database recorded it — which is where created_at and
	// updated_at come from.
	//
	// A second held reservation for one order comes back as a conflict rather
	// than as a duplicate, because the partial unique index is the guard: two
	// retries of one saga step racing must not both take stock.
	Create(ctx context.Context, reservation *domain.Reservation) (*domain.Reservation, error)

	// Update writes a loaded aggregate's status back under its optimistic lock,
	// and fails with a conflict when the version it carries is no longer the
	// stored one. The lines are not rewritten: they are what was held, and
	// nothing may change that after the fact.
	Update(ctx context.Context, reservation *domain.Reservation) (*domain.Reservation, error)

	// FindByID returns the reservation with its lines, or a not-found error.
	FindByID(ctx context.Context, id domain.ReservationID) (*domain.Reservation, error)

	// FindHeldByOrderID returns the live hold an order has, or a not-found
	// error when it has none. A finished reservation is history and does not
	// answer here — an order that meets one is holding nothing.
	FindHeldByOrderID(ctx context.Context, orderID domain.OrderID) (*domain.Reservation, error)

	// ClaimExpired takes at most limit held reservations whose time has run out,
	// and locks them for the rest of the transaction so that a second sweeper
	// takes different ones rather than waiting behind this one.
	//
	// now is passed in rather than read here, so that the moment the rows were
	// selected on is the same moment the domain judges them by.
	ClaimExpired(ctx context.Context, now time.Time, limit int) ([]*domain.Reservation, error)
}
