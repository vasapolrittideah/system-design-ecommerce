package domain

// Event is something that has already happened to an aggregate here, named in
// this service's own vocabulary.
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

// UserRegistered says that an account now exists.
//
// It carries no timestamp. When the account came to exist is the created_at the
// database wrote, decided by one clock rather than by whichever replica handled
// the request, and the adapter stamps the event with it in the same transaction
// as the row.
type UserRegistered struct {
	UserID UserID
	Email  Email
	Roles  []Role
}

// EventName implements Event.
func (UserRegistered) EventName() string { return "UserRegistered" }
