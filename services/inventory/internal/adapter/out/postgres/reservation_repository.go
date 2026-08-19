package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/adapter/out/postgres/sqlc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/port/out"
)

// ReservationRepository implements the driven port over pgx.
//
// It is the only package in the service that knows a reservation is two tables.
type ReservationRepository struct {
	pool *pgxpool.Pool
}

var _ out.ReservationRepository = (*ReservationRepository)(nil)

// NewReservationRepository builds the repository over a pool.
func NewReservationRepository(pool *pgxpool.Pool) *ReservationRepository {
	return &ReservationRepository{pool: pool}
}

func (r *ReservationRepository) queries(ctx context.Context) *sqlc.Queries {
	return sqlc.New(txmanager.From(ctx, r.pool))
}

// Create inserts the reservation and every line it was taken with.
//
// The caller is expected to have opened a transaction, because these are several
// statements describing one aggregate — and because the stock this hold is
// against was decremented in the same one.
func (r *ReservationRepository) Create(
	ctx context.Context,
	reservation *domain.Reservation,
) (*domain.Reservation, error) {
	snapshot := reservation.Snapshot()

	id, err := parseUUID("reservation id", snapshot.ID.String())
	if err != nil {
		return nil, err
	}

	orderID, err := parseUUID("order id", snapshot.OrderID.String())
	if err != nil {
		return nil, err
	}

	queries := r.queries(ctx)

	row, err := queries.CreateReservation(ctx, sqlc.CreateReservationParams{
		ID:        id,
		OrderID:   orderID,
		Status:    snapshot.Status.String(),
		ExpiresAt: snapshot.ExpiresAt,
	})
	if err != nil {
		if isConstraintViolation(err, heldOrderConstraint) {
			// Two retries of one saga step raced and this one lost. The winner's
			// hold is real, so the answer is to ask again rather than to give
			// up — which is what a conflict says and a 500 would not.
			return nil, errorx.New(errorx.KindConflict, "order %s took a reservation concurrently", snapshot.OrderID).
				WithReason("RESERVATION_IN_FLIGHT").
				WithMetadata(map[string]string{"order_id": snapshot.OrderID.String()})
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "create reservation for order %s", snapshot.OrderID)
	}

	lines := make([]sqlc.ReservationLine, 0, len(snapshot.Lines))

	for _, line := range snapshot.Lines {
		lineID, err := parseUUID("reservation line id", domain.NewLineID().String())
		if err != nil {
			return nil, err
		}

		stored, err := queries.CreateReservationLine(ctx, sqlc.CreateReservationLineParams{
			ID:            lineID,
			ReservationID: id,
			Sku:           line.SKU.String(),
			Quantity:      line.Quantity.Int32(),
		})
		if err != nil {
			return nil, errorx.Wrap(err, errorx.KindInternal, "create reservation line %s", line.SKU)
		}

		lines = append(lines, stored)
	}

	return toReservationDomain(&row, lines), nil
}

// Update writes the aggregate's status back under its optimistic lock.
//
// The lines are not rewritten. They are what was held, and the counts already
// recorded that; a statement able to change them would be a way to release a
// quantity that was never taken.
func (r *ReservationRepository) Update(
	ctx context.Context,
	reservation *domain.Reservation,
) (*domain.Reservation, error) {
	snapshot := reservation.Snapshot()

	id, err := parseUUID("reservation id", snapshot.ID.String())
	if err != nil {
		return nil, err
	}

	queries := r.queries(ctx)

	row, err := queries.UpdateReservationStatus(ctx, sqlc.UpdateReservationStatusParams{
		ID:      id,
		Status:  snapshot.Status.String(),
		Version: narrow(snapshot.Version),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The version moved between the read and this write. Not
			// KindNotFound: the caller loaded this aggregate moments ago in the
			// same transaction, so the row existing is not in doubt — and a
			// client told "not found" would stop retrying, where the answer here
			// is to re-read and try again.
			return nil, errorx.New(errorx.KindConflict, "reservation %s was modified concurrently", snapshot.ID).
				WithReason("RESERVATION_MODIFIED")
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "update reservation %s", snapshot.ID)
	}

	lines, err := r.linesOf(ctx, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}

	return toReservationDomain(&row, lines[id]), nil
}

// FindByID returns the reservation with its lines.
func (r *ReservationRepository) FindByID(
	ctx context.Context,
	id domain.ReservationID,
) (*domain.Reservation, error) {
	reservationID, err := parseUUID("reservation id", id.String())
	if err != nil {
		return nil, err
	}

	row, err := r.queries(ctx).GetReservationByID(ctx, reservationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound("reservation %s not found", id)
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "get reservation %s", id)
	}

	return r.withLines(ctx, &row)
}

// FindHeldByOrderID returns the live hold an order has.
//
// A finished reservation does not answer here: the query filters on held, which
// is the same predicate the partial unique index carries, so this is
// single-valued for exactly as long as that index makes it so.
func (r *ReservationRepository) FindHeldByOrderID(
	ctx context.Context,
	orderID domain.OrderID,
) (*domain.Reservation, error) {
	id, err := parseUUID("order id", orderID.String())
	if err != nil {
		return nil, err
	}

	row, err := r.queries(ctx).GetHeldReservationByOrderID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFound("order %s holds no reservation", orderID)
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "get reservation for order %s", orderID)
	}

	return r.withLines(ctx, &row)
}

// ClaimExpired takes and locks a batch of expired holds.
//
// The lock lives as long as the caller's transaction, which is what makes the
// sweep divisible: a second reaper's SKIP LOCKED steps over these rows and takes
// the next ones instead of blocking behind them.
func (r *ReservationRepository) ClaimExpired(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]*domain.Reservation, error) {
	rows, err := r.queries(ctx).ClaimExpiredReservations(ctx, sqlc.ClaimExpiredReservationsParams{
		Now:      now,
		RowLimit: narrow(limit),
	})
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "claim expired reservations")
	}

	if len(rows) == 0 {
		return nil, nil
	}

	ids := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}

	// One query for the whole batch rather than one per reservation: the sweep
	// exists to catch up, and a query per row makes it slower the further behind
	// it is.
	byReservation, err := r.linesOf(ctx, ids)
	if err != nil {
		return nil, err
	}

	reservations := make([]*domain.Reservation, 0, len(rows))
	for i := range rows {
		reservations = append(reservations, toReservationDomain(&rows[i], byReservation[rows[i].ID]))
	}

	return reservations, nil
}

// withLines loads one reservation's lines and assembles the aggregate.
func (r *ReservationRepository) withLines(
	ctx context.Context,
	row *sqlc.Reservation,
) (*domain.Reservation, error) {
	lines, err := r.linesOf(ctx, []uuid.UUID{row.ID})
	if err != nil {
		return nil, err
	}

	return toReservationDomain(row, lines[row.ID]), nil
}

// linesOf loads the lines for a set of reservations, grouped by the reservation
// they belong to.
func (r *ReservationRepository) linesOf(
	ctx context.Context,
	ids []uuid.UUID,
) (map[uuid.UUID][]sqlc.ReservationLine, error) {
	rows, err := r.queries(ctx).GetReservationLinesByReservationIDs(ctx, ids)
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "get lines for %d reservation(s)", len(ids))
	}

	byReservation := make(map[uuid.UUID][]sqlc.ReservationLine, len(ids))
	for i := range rows {
		byReservation[rows[i].ReservationID] = append(byReservation[rows[i].ReservationID], rows[i])
	}

	return byReservation, nil
}

// notFound builds the one not-found this package raises about a reservation.
//
// Wrapped around the domain sentinel rather than built from scratch, because the
// reserving use case branches on it with errors.Is to tell "this order has no
// hold yet" from "the read failed" — and a not-found that only carried a status
// code could not answer that.
func notFound(format string, args ...any) error {
	return errorx.Wrap(domain.ErrReservationNotFound, errorx.KindNotFound, format, args...).
		WithReason("RESERVATION_NOT_FOUND")
}

// toReservationDomain rebuilds the aggregate from its rows.
func toReservationDomain(row *sqlc.Reservation, lines []sqlc.ReservationLine) *domain.Reservation {
	held := make([]domain.Line, 0, len(lines))
	for i := range lines {
		held = append(held, domain.Line{
			SKU:      domain.SKU(lines[i].Sku),
			Quantity: domain.Quantity(lines[i].Quantity),
		})
	}

	return domain.ReconstituteReservation(domain.ReservationSnapshot{
		ID:        domain.ReservationID(row.ID.String()),
		OrderID:   domain.OrderID(row.OrderID.String()),
		Lines:     held,
		Status:    domain.ReservationStatus(row.Status),
		ExpiresAt: row.ExpiresAt,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
		Version:   int(row.Version),
	})
}
