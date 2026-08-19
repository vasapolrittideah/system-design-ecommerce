// Package domain holds the inventory service's aggregates and the rules that
// protect them: what a reservation is, which states it may move between, and how
// long a hold on stock survives before the capacity goes back on the shelf.
//
// The rule this service exists for is deliberately not in here. "Never sell the
// same unit twice" is a statement about concurrent writers, and an aggregate can
// only ever see one of them — a count read into memory is already out of date by
// the time a decision made on it is written back. It lives in the predicate of
// the reserving UPDATE, with a CHECK constraint behind that. What this package
// keeps is the name: ErrInsufficientStock is what that failed write means in the
// warehouse's vocabulary rather than in PostgreSQL's.
package domain

import (
	"cmp"
	"slices"
	"time"
)

// The bounds on a reservation, matching what the proto declares.
const (
	linesMax        = 100
	lineQuantityMax = 10_000
)

// ReservationStatus is where a reservation sits in its lifecycle. The values are
// the strings the status column stores, so the CHECK constraint in the migration
// and this type cannot drift apart in a way that only shows up on a write.
type ReservationStatus string

const (
	// StatusHeld is holding capacity, and doing so until ExpiresAt.
	StatusHeld ReservationStatus = "held"

	// StatusCommitted is sold. The held quantity is gone rather than returned.
	StatusCommitted ReservationStatus = "committed"

	// StatusReleased was given back on purpose — the compensating step of a
	// saga that failed further along.
	StatusReleased ReservationStatus = "released"

	// StatusExpired was given back by the reaper, because nobody committed it
	// in time. Distinct from released so that a stranded saga is visible as one
	// instead of looking like a compensation somebody ran.
	StatusExpired ReservationStatus = "expired"
)

// String returns the stored form.
func (s ReservationStatus) String() string { return string(s) }

// Line is one SKU's share of a reservation.
//
// It has no identifier of its own in this package. A line is meaningful only as
// part of its reservation, and the row's uuid is the repository's business.
type Line struct {
	SKU      SKU
	Quantity Quantity
}

// Reservation is the aggregate: one order's claim on stock, and what happens to
// that claim.
//
// Its fields are unexported because several of them are invariants rather than
// data. The lines are summed per SKU and sorted, the status is never a value
// this package cannot name, and no transition happens that the state machine
// below does not allow — none of which survives callers assigning to fields.
//
// The whole order is one reservation rather than one per line, because it is
// released and committed as a unit: a saga compensating line by line could leave
// half an order holding capacity nobody will ever buy.
type Reservation struct {
	id      ReservationID
	orderID OrderID
	lines   []Line
	status  ReservationStatus

	expiresAt time.Time

	// Set by the database on insert and read back, never chosen here.
	createdAt time.Time
	updatedAt time.Time

	// version is the optimistic lock: the value an UPDATE must carry and bump,
	// so a commit and a release arriving together cannot both succeed.
	version int
}

// NewReservation builds a hold that has never been persisted.
//
// The lines are canonicalised rather than taken as given: repeats of one SKU are
// summed, and the result is sorted. Both matter beyond tidiness.
//
// Summing is what a caller means — a cart that added the same shirt twice is
// describing one quantity — and it is also what the UNIQUE constraint on
// (reservation_id, sku) requires.
//
// Sorting is what stops two concurrent reservations deadlocking. Reserving takes
// a row lock per SKU, so an order for (A, B) and one for (B, A) running together
// would each hold what the other needs next; taking them in one order everywhere
// means the second waits instead. It costs nothing and the failure it prevents
// appears only under load.
func NewReservation(
	orderID OrderID,
	lines []Line,
	now time.Time,
	ttl time.Duration,
) (*Reservation, error) {
	if ttl <= 0 {
		// Not a caller's mistake — the TTL is this service's own configuration —
		// but refusing here is what stops a misconfigured deployment writing
		// holds that were already expired when they were taken.
		return nil, ValidationError{Field: "ttl", Message: "must be positive"}
	}

	canonical, err := canonicalLines(lines)
	if err != nil {
		return nil, err
	}

	return &Reservation{
		id:        NewReservationID(),
		orderID:   orderID,
		lines:     canonical,
		status:    StatusHeld,
		expiresAt: now.Add(ttl),
		version:   1,
	}, nil
}

// Commit turns the hold into a sale.
//
// It reports whether this call is what moved the reservation. A second commit of
// the same reservation succeeds and reports false, and a caller that took the
// stock down anyway would sell the goods twice — every step of a saga is retried
// eventually. When this package raises domain events, that bool is what
// PullEvents will replace.
//
// A hold whose time has run out is refused even while the reaper has not reached
// it, because the capacity is already promised to whoever asks next.
func (r *Reservation) Commit(now time.Time) (bool, error) {
	switch r.status {
	case StatusCommitted:
		return false, nil
	case StatusReleased:
		return false, ErrReservationReleased
	case StatusExpired:
		return false, ErrReservationExpired
	case StatusHeld:
		if r.hasExpired(now) {
			return false, ErrReservationExpired
		}

		r.status = StatusCommitted

		return true, nil
	default:
		// A row stored before a state was named, which Reconstitute loads rather
		// than refuses. Refusing to act on it is the only safe answer: the
		// alternative is guessing what a future version of this service meant.
		return false, ErrReservationNotFound
	}
}

// Release gives the held stock back, and reports whether this call is what moved
// the reservation.
//
// An already-expired hold reports false with no error: the reaper has returned
// the capacity, which is exactly what the caller wanted done. A committed one is
// refused, because putting the number back would invent stock that is not on a
// shelf.
func (r *Reservation) Release() (bool, error) {
	switch r.status {
	case StatusReleased, StatusExpired:
		return false, nil
	case StatusCommitted:
		return false, ErrReservationCommitted
	case StatusHeld:
		r.status = StatusReleased

		return true, nil
	default:
		return false, ErrReservationNotFound
	}
}

// Expire is the reaper's transition, and the only one that judges the clock
// rather than being told about it.
//
// It is separate from Release so that a hold nobody came back for is
// distinguishable afterwards from one a saga compensated — the first is a
// stranded workflow worth alerting on, the second is the system working.
func (r *Reservation) Expire(now time.Time) (bool, error) {
	if r.status != StatusHeld {
		return false, nil
	}

	if !r.hasExpired(now) {
		return false, ErrReservationNotExpired
	}

	r.status = StatusExpired

	return true, nil
}

// Matches reports whether this reservation holds exactly what lines describe.
//
// It is what an idempotent retry is checked against: a saga asking again for the
// hold it already has gets that hold back, while the same order id carrying a
// different basket is a caller that changed its mind about an order it had
// already committed to — which is a conflict rather than a silent replacement,
// because the stock for the old lines is spoken for either way.
//
// lines are compared as this package stores them, so a caller passes what
// NewReservation produced rather than what arrived on the wire.
func (r *Reservation) Matches(lines []Line) bool {
	return slices.Equal(r.lines, lines)
}

// ID returns the reservation's identifier.
func (r *Reservation) ID() ReservationID { return r.id }

// OrderID returns the order this hold was taken for.
func (r *Reservation) OrderID() OrderID { return r.orderID }

// Lines returns a copy, summed per SKU and sorted by it, so a caller ranging
// over them cannot rewrite what was held.
func (r *Reservation) Lines() []Line { return slices.Clone(r.lines) }

// Status returns the lifecycle state.
func (r *Reservation) Status() ReservationStatus { return r.status }

// ExpiresAt returns when a held reservation stops being honoured.
func (r *Reservation) ExpiresAt() time.Time { return r.expiresAt }

// CreatedAt is the zero time until the row has been written.
func (r *Reservation) CreatedAt() time.Time { return r.createdAt }

// UpdatedAt is the zero time until the row has been written.
func (r *Reservation) UpdatedAt() time.Time { return r.updatedAt }

// Version is the value an UPDATE must carry to win the optimistic lock.
func (r *Reservation) Version() int { return r.version }

// hasExpired reports whether the hold has run out of time. The boundary is
// inclusive: at exactly expires_at the reaper's own predicate already selects
// this row, and the two must agree or a commit succeeds against capacity the
// sweep is in the middle of returning.
func (r *Reservation) hasExpired(now time.Time) bool {
	return !now.Before(r.expiresAt)
}

// canonicalLines sums repeats of one SKU and sorts the result.
func canonicalLines(lines []Line) ([]Line, error) {
	if len(lines) == 0 {
		return nil, ValidationError{Field: "lines", Message: "is required"}
	}

	if len(lines) > linesMax {
		return nil, ValidationError{Field: "lines", Message: "has more than 100 entries"}
	}

	summed := make(map[SKU]Quantity, len(lines))

	for _, line := range lines {
		if line.SKU == "" {
			return nil, ValidationError{Field: "sku", Message: "is required"}
		}

		if line.Quantity <= 0 {
			return nil, ValidationError{Field: "quantity", Message: "must be at least 1"}
		}

		// Checked against the sum rather than the line, because two lines of
		// 9000 are a request for 18000 and the bound is on what gets held.
		if summed[line.SKU]+line.Quantity > lineQuantityMax {
			return nil, ValidationError{Field: "quantity", Message: "totals more than 10000 for one sku"}
		}

		summed[line.SKU] += line.Quantity
	}

	canonical := make([]Line, 0, len(summed))
	for sku, quantity := range summed {
		canonical = append(canonical, Line{SKU: sku, Quantity: quantity})
	}

	slices.SortFunc(canonical, func(a, b Line) int { return cmp.Compare(a.SKU, b.SKU) })

	return canonical, nil
}

// ReservationSnapshot is the whole state of a reservation as it is stored, so a
// repository can write the rows and rebuild the aggregate without its fields
// being exported to everything else that imports this package.
type ReservationSnapshot struct {
	ID        ReservationID
	OrderID   OrderID
	Lines     []Line
	Status    ReservationStatus
	ExpiresAt time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int
}

// ReconstituteReservation rebuilds a reservation from storage.
//
// It validates nothing, and that is deliberate: a bound tightened afterwards
// must not make existing holds unreadable — including the per-SKU ceiling, which
// a reservation taken before it existed may well break. New values come in
// through NewReservation, which does validate.
func ReconstituteReservation(s ReservationSnapshot) *Reservation {
	return &Reservation{
		id:        s.ID,
		orderID:   s.OrderID,
		lines:     slices.Clone(s.Lines),
		status:    s.Status,
		expiresAt: s.ExpiresAt,
		createdAt: s.CreatedAt,
		updatedAt: s.UpdatedAt,
		version:   s.Version,
	}
}

// Snapshot returns the reservation's state for a repository to persist.
func (r *Reservation) Snapshot() ReservationSnapshot {
	return ReservationSnapshot{
		ID:        r.id,
		OrderID:   r.orderID,
		Lines:     slices.Clone(r.lines),
		Status:    r.status,
		ExpiresAt: r.expiresAt,
		CreatedAt: r.createdAt,
		UpdatedAt: r.updatedAt,
		Version:   r.version,
	}
}
