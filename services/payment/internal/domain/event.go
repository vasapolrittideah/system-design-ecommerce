package domain

// Event is something that has already happened to an attempt to collect,
// named in this service's own vocabulary.
//
// It is a plain interface with no dependencies, because this package may not
// import the transport an event eventually travels on. An adapter maps one of
// these to the generated message and writes it to the outbox; nothing in here
// knows that a topic exists.
type Event interface {
	// EventName is what consumers dispatch on. It is declared by a method
	// rather than by importing a shared enum for the same reason a domain error
	// declares its kind that way — a method signature is not a dependency.
	//
	// It is also API: a consumer branches on this string, so renaming one is a
	// breaking change that no compiler reports.
	EventName() string
}

// PaymentSucceeded says the money for an order is in.
//
// The order service consumes it and marks its order paid, which is what
// eventually turns the hold on stock into a sale. This service consumes nothing
// back: what an order does about having been paid is the order's business.
//
// It carries the amount so a consumer can compare what was collected against
// what was owed, which is how a mismatch with the provider gets noticed at all,
// and the provider's reference so an order can be traced to a line in the
// provider's dashboard without a second read.
type PaymentSucceeded struct {
	PaymentID         PaymentID
	OrderID           OrderID
	UserID            UserID
	Amount            Money
	ProviderReference ProviderReference
}

// EventName implements Event.
func (PaymentSucceeded) EventName() string { return "PaymentSucceeded" }

// PaymentFailed says one attempt is over and no money moved.
//
// It does not say the order is dead. A customer whose card was declined may try
// another while their stock is still held, so giving up is a decision about an
// order rather than about this attempt — and therefore the order service's to
// make, on its own timeout rather than on this event.
type PaymentFailed struct {
	PaymentID PaymentID
	OrderID   OrderID
	UserID    UserID
	Amount    Money

	// Reason is the provider's own words and may be empty. No consumer may
	// branch on it: the set of these belongs to the provider and changes
	// without this system being told.
	Reason string
}

// EventName implements Event.
func (PaymentFailed) EventName() string { return "PaymentFailed" }
