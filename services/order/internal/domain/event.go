package domain

// Event is something that has already happened to an order, named in this
// service's own vocabulary.
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

// OrderPlaced says that an order exists and is waiting for payment, with stock
// already held for it.
//
// This service consumes it back itself. The step that turns the hold into a
// sale is a call to inventory, and a call made straight after the transaction
// commits is a write nothing would retry if the process died between the two —
// so the trigger is read off the topic instead, where it is as durable as the
// order.
//
// It carries no timestamp. When the order came to exist is the created_at the
// database wrote, decided by one clock rather than by whichever replica served
// the request, and the adapter stamps the event with it.
type OrderPlaced struct {
	OrderID       OrderID
	UserID        UserID
	ReservationID ReservationID
	Lines         []OrderLine
	Total         Money
}

// EventName implements Event.
func (OrderPlaced) EventName() string { return "OrderPlaced" }
