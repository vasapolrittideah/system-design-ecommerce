package domain

import "fmt"

// ValidationError reports a value this package refused.
//
// Its kind is declared structurally — ErrorKind returns one of the strings
// pkg/errorx knows, and a method signature is not an import — so errorx.ToGRPC
// answers InvalidArgument for it while this package still depends on nothing but
// the standard library.
type ValidationError struct {
	// Field is the name the caller used, so a client can point at the input it
	// got wrong. It is the proto field name where there is one.
	Field string

	// Message says what is wrong with it, and never repeats the value.
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ErrorKind maps this error to errorx.KindInvalidInput without importing it.
func (e ValidationError) ErrorKind() string { return "invalid_input" }

// sentinelError is a failure that is the same every time it happens, so there is
// nothing per-occurrence to carry and one package-level value will do.
//
// It declares its kind the way ValidationError does. A sentinel from errors.New
// would declare none, resolve to Internal, and have the message that said
// otherwise scrubbed.
type sentinelError struct {
	msg  string
	kind string
}

func (e *sentinelError) Error() string     { return e.msg }
func (e *sentinelError) ErrorKind() string { return e.kind }

// The refusals this service can produce. They are values rather than types
// because a caller has to branch on them with errors.Is, and because the
// boundary turns each one into its own reason code — the string a client
// actually writes an if against.
//
// All but the not-found ones are conflicts: the request was well-formed and the
// warehouse is simply not in a state where it can be honoured, which is 409 and
// not 400.
var (
	// ErrInsufficientStock is the answer to a reservation nobody can fill.
	//
	// It is named here even though the aggregate is not what raises it. The
	// check that matters happens in a WHERE clause — see the package comment —
	// and this is what gives the failure a name in this service's own
	// vocabulary rather than leaving it as a row count.
	ErrInsufficientStock = &sentinelError{
		msg:  "not enough stock to reserve",
		kind: "conflict",
	}

	// ErrStockItemNotFound is a SKU this service does not track. Distinct from
	// having none left: a caller that meets it has asked about something the
	// warehouse has never heard of, and re-trying will not change that.
	ErrStockItemNotFound = &sentinelError{
		msg:  "sku is not tracked",
		kind: "not_found",
	}

	// ErrReservationNotFound is an id or an order that holds nothing.
	ErrReservationNotFound = &sentinelError{
		msg:  "reservation does not exist",
		kind: "not_found",
	}

	// ErrReservationExpired refuses a commit against a hold that ran out of
	// time, whether or not the reaper has caught up with it yet. The capacity
	// is already promised to whoever asks next, and committing on the strength
	// of the sweep being slow is how the same unit gets sold twice.
	ErrReservationExpired = &sentinelError{
		msg:  "reservation has expired",
		kind: "conflict",
	}

	// ErrReservationCommitted refuses a release against a sale. The goods have
	// left the building; putting the number back would invent stock that is not
	// on a shelf.
	ErrReservationCommitted = &sentinelError{
		msg:  "reservation was already committed",
		kind: "conflict",
	}

	// ErrReservationReleased refuses a commit against a hold that was given
	// back. The stock it was holding may already belong to another order.
	ErrReservationReleased = &sentinelError{
		msg:  "reservation was already released",
		kind: "conflict",
	}

	// ErrReservationNotExpired refuses sweeping a hold that still has time.
	// Unreachable through the reaper, which only claims rows the database has
	// already judged expired, and kept because the day it is reachable the
	// alternative is capacity taken from an order that is still being paid for.
	ErrReservationNotExpired = &sentinelError{
		msg:  "reservation has not expired",
		kind: "conflict",
	}
)
