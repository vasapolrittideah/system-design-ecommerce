// Package domain holds the order service's aggregate and the rules that protect
// it: what makes a set of lines an order, what an order totals, and which
// transitions between its states are allowed.
//
// It is also where the checkout saga's state machine lives. The saga is
// orchestrated from the use case, but every move it makes is a method here, so
// the question "can this order be paid?" has exactly one answer and it is not
// in a consumer.
package domain

import (
	"math"
	"slices"
	"time"
)

// maxLines is the most lines one order may carry, matching the bound the proto
// declares.
const maxLines = 100

// Status is where an order sits in its flow.
//
// A string rather than an integer, so a row is readable without a lookup table
// and a value that stops being used leaves no gap anyone has to remember.
type Status string

// The states an order can be in.
const (
	// StatusPendingPayment is persisted, stock held, waiting for money.
	StatusPendingPayment Status = "pending_payment"

	// StatusPaid is the money in. The hold on stock is still a hold: turning it
	// into a sale is the next step of the saga.
	StatusPaid Status = "paid"

	// StatusCancelled is terminal. Whatever was reserved has been given back,
	// and a customer who wants it after all places a new order — the stock this
	// one held is somebody else's by now.
	StatusCancelled Status = "cancelled"
)

// Order is the aggregate: one purchase, its lines, and how far it has got.
//
// Its fields are unexported because three of them are invariants rather than
// data. Every line is priced in one currency, so a total is a single Money; the
// total is the sum of the lines, so no row can disagree with its own children;
// and the status only ever moves the ways the methods below allow.
type Order struct {
	id            OrderID
	userID        UserID
	status        Status
	lines         []OrderLine
	total         Money
	reservationID ReservationID

	// Set by the database on insert and read back, never chosen here: two
	// replicas disagree about the current time by more than the ordering of two
	// orders is worth.
	createdAt time.Time
	updatedAt time.Time

	// version is the optimistic lock: the value an UPDATE must carry and bump,
	// so a concurrent writer affects zero rows and finds out.
	version int

	// events raised by this instance and not yet taken. They leave through
	// PullEvents, and the repository writes them to the outbox in the
	// transaction that persists the row.
	events []Event
}

// NewOrder places an order against stock that is already held.
//
// The reservation is taken before this is called and its id is required here,
// which is the shape of the rule rather than an ordering detail: an order that
// existed without a hold would be a promise to sell something nobody set aside.
//
// The identifier is minted by NewOrderID before either, because the reservation
// has to name the order it is for.
func NewOrder(id OrderID, userID UserID, reservationID ReservationID, lines []OrderLine) (*Order, error) {
	switch {
	case id == "":
		return nil, ValidationError{Field: "id", Message: "is required"}
	case userID == "":
		return nil, ValidationError{Field: "user_id", Message: "is required"}
	case reservationID == "":
		return nil, ValidationError{Field: "reservation_id", Message: "is required"}
	}

	if len(lines) == 0 {
		return nil, ValidationError{Field: "lines", Message: "an order has at least one line"}
	}
	if len(lines) > maxLines {
		return nil, ValidationError{Field: "lines", Message: "is above the per-order maximum"}
	}

	// Two lines naming one SKU are refused rather than summed. A cart that did
	// that is describing one quantity twice, and if the two carry different
	// prices there is no way to tell which one the customer agreed to.
	seen := make(map[SKU]struct{}, len(lines))
	for _, line := range lines {
		if _, duplicate := seen[line.sku]; duplicate {
			return nil, ValidationError{Field: "lines", Message: "names the same sku twice"}
		}
		seen[line.sku] = struct{}{}
	}

	total, err := sum(lines)
	if err != nil {
		return nil, err
	}

	order := &Order{
		id:            id,
		userID:        userID,
		status:        StatusPendingPayment,
		lines:         slices.Clone(lines),
		total:         total,
		reservationID: reservationID,
		version:       1,
	}
	order.events = append(order.events, OrderPlaced{
		OrderID:       order.id,
		UserID:        order.userID,
		ReservationID: order.reservationID,
		Lines:         slices.Clone(order.lines),
		Total:         order.total,
	})

	return order, nil
}

// sum totals the lines and holds the one-currency invariant.
//
// An order priced in two currencies has no total, and the failure it would
// otherwise become is a number that silently added baht to dollars.
func sum(lines []OrderLine) (Money, error) {
	currency := lines[0].unitPrice.currency

	var amount int64
	for _, line := range lines {
		if line.unitPrice.currency != currency {
			return Money{}, ValidationError{
				Field:   "lines",
				Message: "are priced in more than one currency",
			}
		}

		subtotal, err := line.Subtotal()
		if err != nil {
			return Money{}, err
		}

		if amount > math.MaxInt64-subtotal.amountMinor {
			return Money{}, ErrTotalOutOfRange
		}
		amount += subtotal.amountMinor
	}

	return Money{amountMinor: amount, currency: currency}, nil
}

// OrderSnapshot is the whole state of an order as it is stored, so a repository
// can write it and rebuild it without the aggregate's fields being exported to
// everything else that imports this package.
type OrderSnapshot struct {
	ID            OrderID
	UserID        UserID
	Status        Status
	Lines         []OrderLine
	Total         Money
	ReservationID ReservationID
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Version       int
}

// ReconstituteOrder rebuilds an order from storage.
//
// It validates nothing, and that is deliberate: a rule tightened afterwards must
// not make existing orders unreadable, and an order that cannot be loaded is one
// nobody can cancel or refund either.
func ReconstituteOrder(s OrderSnapshot) *Order {
	return &Order{
		id:            s.ID,
		userID:        s.UserID,
		status:        s.Status,
		lines:         s.Lines,
		total:         s.Total,
		reservationID: s.ReservationID,
		createdAt:     s.CreatedAt,
		updatedAt:     s.UpdatedAt,
		version:       s.Version,
	}
}

// Snapshot returns the order's state for a repository to persist.
func (o *Order) Snapshot() OrderSnapshot {
	return OrderSnapshot{
		ID:            o.id,
		UserID:        o.userID,
		Status:        o.status,
		Lines:         slices.Clone(o.lines),
		Total:         o.total,
		ReservationID: o.reservationID,
		CreatedAt:     o.createdAt,
		UpdatedAt:     o.updatedAt,
		Version:       o.version,
	}
}

// MarkPaid records that the money is in.
//
// It reports whether this call is what moved the order. A second delivery of
// the same payment succeeds and reports false, because every step of a saga is
// retried eventually and a caller that acted on the strength of a bare error
// would either charge twice or abandon a paid order.
//
// A cancelled order refuses: its stock has been given back, and taking money on
// the strength of a hold that no longer exists is the one outcome nobody can
// undo cheaply.
func (o *Order) MarkPaid() (bool, error) {
	switch o.status {
	case StatusPaid:
		return false, nil
	case StatusCancelled:
		return false, ErrOrderCancelled
	}

	o.status = StatusPaid

	return true, nil
}

// Cancel ends the order.
//
// It reports whether this call is what moved it, for the reason MarkPaid does:
// compensation runs more than once.
//
// A paid order refuses. Undoing a payment is a refund — a decision about money,
// with a provider on the other end of it — and not a transition this aggregate
// may make on its own.
func (o *Order) Cancel() (bool, error) {
	switch o.status {
	case StatusCancelled:
		return false, nil
	case StatusPaid:
		return false, ErrOrderPaid
	}

	o.status = StatusCancelled

	return true, nil
}

// PullEvents returns the events this instance has raised and forgets them, so a
// repository that persists the same aggregate twice does not publish them twice.
//
// An order read back by ReconstituteOrder has none: the events belong to the
// change that was just made, not to the state that was read.
func (o *Order) PullEvents() []Event {
	pulled := o.events
	o.events = nil

	return pulled
}

// ID returns the order's identifier, which is also the id its stock is reserved
// under.
func (o *Order) ID() OrderID { return o.id }

// UserID returns who placed it. Whether a particular caller may see it is a
// question for the use case, which knows who is asking.
func (o *Order) UserID() UserID { return o.userID }

// Status returns where the order sits in its flow.
func (o *Order) Status() Status { return o.status }

// Lines returns a copy, so a caller ranging over them cannot rewrite what was
// bought.
func (o *Order) Lines() []OrderLine { return slices.Clone(o.lines) }

// Total returns what the customer agreed to pay.
func (o *Order) Total() Money { return o.total }

// ReservationID returns inventory's hold on the stock for this order.
func (o *Order) ReservationID() ReservationID { return o.reservationID }

// CreatedAt is the zero time until the row has been written.
func (o *Order) CreatedAt() time.Time { return o.createdAt }

// UpdatedAt is the zero time until the row has been written.
func (o *Order) UpdatedAt() time.Time { return o.updatedAt }

// Version is the value an UPDATE must carry to win the optimistic lock.
func (o *Order) Version() int { return o.version }
