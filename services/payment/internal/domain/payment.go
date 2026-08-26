// Package domain holds the payment service's aggregate and the rules that
// protect it: what one attempt to collect money is, which of its outcomes may
// follow which, and what the provider is allowed to tell us about it after the
// fact.
//
// The money itself moves somewhere else. What lives here is the record of
// having asked for it, and the rule that record exists to hold: an attempt has
// exactly one ending, and nothing a provider says afterwards may give it a
// second one.
package domain

import "time"

// Status is how far one attempt to collect has got.
//
// A string rather than an integer, so a row is readable without a lookup table
// and a value that stops being used leaves no gap anyone has to remember.
type Status string

// The states an attempt can be in.
const (
	// StatusPending is sent to the provider with no money yet. The customer may
	// still have something to do, or the provider may simply not have answered.
	StatusPending Status = "pending"

	// StatusSucceeded is the money in. Terminal.
	StatusSucceeded Status = "succeeded"

	// StatusFailed is the attempt over with no money moved. Terminal for the
	// attempt and not for the order: the customer may try again while the stock
	// held for it has not expired.
	StatusFailed Status = "failed"
)

// Payment is the aggregate: one attempt to collect the money for an order.
//
// One attempt, not one order. A declined card is a finished attempt and trying
// again is a new one, which is why nothing here is keyed on the order and why
// this aggregate never asks whether the order has been paid by some other
// attempt — that is the order's own state machine, and it is what refuses the
// second one.
//
// Its fields are unexported because two of them are invariants rather than
// data: the status only ever moves the ways the methods below allow, and the
// amount is what was sent to the provider rather than whatever the order says
// now.
type Payment struct {
	id      PaymentID
	orderID OrderID
	userID  UserID
	status  Status
	amount  Money
	method  Method

	// providerReference is what the provider calls this charge. Empty until it
	// has answered, which is a state that has to be representable: the row is
	// written before the call so that a call which times out has left a record
	// of itself.
	providerReference ProviderReference

	// failureReason is the provider's own words, empty unless status is failed.
	failureReason string

	// Set by the database on insert and read back, never chosen here: two
	// replicas disagree about the current time by more than the ordering of two
	// attempts is worth.
	createdAt time.Time
	updatedAt time.Time

	// version is the optimistic lock: the value an UPDATE must carry and bump,
	// so a concurrent writer affects zero rows and finds out. It matters more
	// here than anywhere else in this system — a callback and a reconciliation
	// arriving together are two writers settling one charge.
	version int

	// events raised by this instance and not yet taken. They leave through
	// PullEvents, and the repository drains them into the outbox in the
	// transaction that persists the row.
	events []Event
}

// NewPayment starts an attempt to collect for an order.
//
// It raises no event. An attempt that has begun is not a fact anybody outside
// this service can act on — the order is already waiting for payment and knows
// it — and the facts worth publishing are the two endings.
//
// The amount is required to be positive: a charge for nothing is not a charge,
// and a provider asked to make one answers in its own way rather than in ours.
func NewPayment(id PaymentID, orderID OrderID, userID UserID, amount Money, method Method) (*Payment, error) {
	switch {
	case id == "":
		return nil, ValidationError{Field: "id", Message: "is required"}
	case orderID == "":
		return nil, ValidationError{Field: "order_id", Message: "is required"}
	case userID == "":
		return nil, ValidationError{Field: "user_id", Message: "is required"}
	}

	if amount.currency == "" {
		return nil, ValidationError{Field: "amount", Message: "has no currency"}
	}
	if amount.amountMinor <= 0 {
		return nil, ValidationError{Field: "amount", Message: "is not a positive amount"}
	}

	return &Payment{
		id:      id,
		orderID: orderID,
		userID:  userID,
		status:  StatusPending,
		amount:  amount,
		method:  method,
		version: 1,
	}, nil
}

// PaymentSnapshot is the whole state of an attempt as it is stored, so a
// repository can write it and rebuild it without the aggregate's fields being
// exported to everything else that imports this package.
type PaymentSnapshot struct {
	ID                PaymentID
	OrderID           OrderID
	UserID            UserID
	Status            Status
	Amount            Money
	Method            Method
	ProviderReference ProviderReference
	FailureReason     string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Version           int
}

// ReconstitutePayment rebuilds an attempt from storage.
//
// It validates nothing, and that is deliberate: a rule tightened afterwards
// must not make existing rows unreadable, and an attempt that cannot be loaded
// is one nobody can reconcile against the provider either — which is the exact
// situation the rule would have been tightened to prevent.
func ReconstitutePayment(s PaymentSnapshot) *Payment {
	return &Payment{
		id:                s.ID,
		orderID:           s.OrderID,
		userID:            s.UserID,
		status:            s.Status,
		amount:            s.Amount,
		method:            s.Method,
		providerReference: s.ProviderReference,
		failureReason:     s.FailureReason,
		createdAt:         s.CreatedAt,
		updatedAt:         s.UpdatedAt,
		version:           s.Version,
	}
}

// Snapshot returns the attempt's state for a repository to persist.
func (p *Payment) Snapshot() PaymentSnapshot {
	return PaymentSnapshot{
		ID:                p.id,
		OrderID:           p.orderID,
		UserID:            p.userID,
		Status:            p.status,
		Amount:            p.amount,
		Method:            p.method,
		ProviderReference: p.providerReference,
		FailureReason:     p.failureReason,
		CreatedAt:         p.createdAt,
		UpdatedAt:         p.updatedAt,
		Version:           p.version,
	}
}

// AttachProviderReference records what the provider calls this charge, once it
// has answered the call that created it.
//
// It reports whether this call is what set it. Recording the same reference
// again succeeds and reports false, because the call that produces it is
// retried after an ambiguous timeout and the second answer is the same charge.
// A *different* reference is refused: two references mean two charges, and this
// aggregate can only ever describe one of them.
//
// It raises nothing. Which identifier a provider chose is not a fact about the
// money, and the events that are carry the reference with them.
func (p *Payment) AttachProviderReference(reference ProviderReference) (bool, error) {
	if reference == "" {
		return false, ValidationError{Field: "provider_reference", Message: "is required"}
	}

	if p.providerReference != "" {
		if p.providerReference != reference {
			return false, ErrProviderReferenceConflict
		}

		return false, nil
	}

	p.providerReference = reference

	return true, nil
}

// Succeed records that the money is in and raises PaymentSucceeded.
//
// It reports whether this call is what settled the attempt. A redelivered
// callback succeeds and reports false, because a provider redelivers and a
// caller that acted on the strength of a bare error would either publish the
// fact twice or treat a collected payment as broken. Nothing is raised on that
// path: the fact is already on the topic.
//
// A failed attempt refuses. A provider reporting success for a charge it
// declined is a contradiction rather than a late answer, and the safe response
// is to stop and let somebody look rather than to take the money.
func (p *Payment) Succeed(reference ProviderReference) (bool, error) {
	switch p.status {
	case StatusSucceeded:
		// The reference still has to agree. A second success naming a different
		// charge is the same contradiction as two references on one attempt.
		if _, err := p.AttachProviderReference(reference); err != nil {
			return false, err
		}

		return false, nil
	case StatusFailed:
		return false, ErrPaymentFailed
	}

	if _, err := p.AttachProviderReference(reference); err != nil {
		return false, err
	}

	p.status = StatusSucceeded
	p.events = append(p.events, PaymentSucceeded{
		PaymentID:         p.id,
		OrderID:           p.orderID,
		UserID:            p.userID,
		Amount:            p.amount,
		ProviderReference: p.providerReference,
	})

	return true, nil
}

// Fail ends the attempt with no money moved and raises PaymentFailed.
//
// It reports whether this call is what settled it, for the reason Succeed does:
// a provider redelivers.
//
// A succeeded attempt refuses. Reversing money that has settled is a refund or
// a chargeback — a second movement, with a provider on the other end of it —
// and not a transition this aggregate may make on being told the first one
// failed.
//
// The reason is the provider's own words and may be empty: a provider that
// declined without saying why has still declined.
func (p *Payment) Fail(reason string) (bool, error) {
	switch p.status {
	case StatusFailed:
		return false, nil
	case StatusSucceeded:
		return false, ErrPaymentSucceeded
	}

	p.status = StatusFailed
	p.failureReason = reason
	p.events = append(p.events, PaymentFailed{
		PaymentID: p.id,
		OrderID:   p.orderID,
		UserID:    p.userID,
		Amount:    p.amount,
		Reason:    reason,
	})

	return true, nil
}

// PullEvents returns the events this instance has raised and forgets them, so a
// repository that persists the same aggregate twice does not publish them twice.
//
// An attempt read back by ReconstitutePayment has none: the events belong to the
// change that was just made, not to the state that was read.
func (p *Payment) PullEvents() []Event {
	pulled := p.events
	p.events = nil

	return pulled
}

// ID returns the attempt's identifier, which is also the idempotency key the
// provider was given.
func (p *Payment) ID() PaymentID { return p.id }

// OrderID returns what is being collected for.
func (p *Payment) OrderID() OrderID { return p.orderID }

// UserID returns who is paying. Whether a particular caller may see this
// attempt is a question for the use case, which knows who is asking.
func (p *Payment) UserID() UserID { return p.userID }

// Status returns how far the attempt has got.
func (p *Payment) Status() Status { return p.status }

// Amount returns what was sent to the provider, which is what the order totalled
// when the attempt started.
func (p *Payment) Amount() Money { return p.amount }

// Method returns how the customer chose to pay, empty for the provider's
// default.
func (p *Payment) Method() Method { return p.method }

// ProviderReference returns what the provider calls this charge, empty until it
// has answered.
func (p *Payment) ProviderReference() ProviderReference { return p.providerReference }

// FailureReason returns the provider's own words, empty unless the attempt
// failed.
func (p *Payment) FailureReason() string { return p.failureReason }

// CreatedAt is the zero time until the row has been written.
func (p *Payment) CreatedAt() time.Time { return p.createdAt }

// UpdatedAt is the zero time until the row has been written.
func (p *Payment) UpdatedAt() time.Time { return p.updatedAt }

// Version is the value an UPDATE must carry to win the optimistic lock.
func (p *Payment) Version() int { return p.version }
