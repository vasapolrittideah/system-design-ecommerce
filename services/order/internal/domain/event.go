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
// Nothing has been sold yet and no money has moved. What the order becomes is
// said by OrderPaid or OrderCancelled, and it is those two that this service
// consumes back.
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

// OrderPaid says the money for an order is in.
//
// This service consumes it back itself. The step that turns the hold on stock
// into a sale is a call to inventory, and a call made straight after the
// transaction commits is a write nothing would retry if the process died
// between the two — so the trigger is read off the topic instead, where it is
// as durable as the payment.
//
// It is deliberately this event and not OrderPlaced that carries that job.
// Committing a hold is selling the goods, and doing it before the money is in
// leaves a failed payment with nothing to give back: inventory refuses to
// release a reservation it has already committed.
type OrderPaid struct {
	OrderID       OrderID
	UserID        UserID
	ReservationID ReservationID
	Total         Money
}

// EventName implements Event.
func (OrderPaid) EventName() string { return "OrderPaid" }

// OrderCancelled says an order will not be fulfilled and whatever it held is to
// be given back.
//
// This service consumes it back itself too, for the same durability reason:
// releasing the reservation is the compensating step of checkout, and it
// belongs on the topic rather than in whichever process decided the order was
// over.
//
// It carries no reason. Why an order ended is a fact about the payment or the
// timeout that ended it, published by whoever knew it; this one says only that
// the stock is free again.
type OrderCancelled struct {
	OrderID       OrderID
	UserID        UserID
	ReservationID ReservationID
}

// EventName implements Event.
func (OrderCancelled) EventName() string { return "OrderCancelled" }
