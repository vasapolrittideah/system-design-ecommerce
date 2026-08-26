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
var (
	// ErrOrderNotFound is an id nobody has. It is also the answer to reading
	// somebody else's order: telling a caller that an order exists but is not
	// theirs is telling them something about another customer.
	ErrOrderNotFound = &sentinelError{
		msg:  "order does not exist",
		kind: "not_found",
	}

	// ErrOrderCancelled refuses a payment against an order that will not
	// proceed. The stock it held has been given back and may be somebody
	// else's by now, so the money must not be taken on the strength of it.
	ErrOrderCancelled = &sentinelError{
		msg:  "order was cancelled",
		kind: "conflict",
	}

	// ErrOrderPaid refuses cancelling an order the customer has paid for.
	// Undoing that is a refund — a decision about money, with a provider on the
	// other end of it — and not a state this aggregate may reach on its own.
	ErrOrderPaid = &sentinelError{
		msg:  "order was already paid",
		kind: "conflict",
	}

	// ErrOrderNotExpired refuses expiring an order whose payment window has not
	// run out. It is what keeps a sweep whose predicate drifted from cancelling
	// orders somebody is still paying for, and it is a conflict rather than an
	// invalid input: the caller asked for something reasonable about a real
	// order, and the answer is not yet.
	ErrOrderNotExpired = &sentinelError{
		msg:  "order's payment window has not run out",
		kind: "conflict",
	}

	// ErrTotalOutOfRange refuses a total that does not fit in the column that
	// stores it. Unreachable through the request bounds the contract declares,
	// and kept because the alternative to a refusal is an amount that wrapped
	// and a charge for the wrong money.
	ErrTotalOutOfRange = &sentinelError{
		msg:  "order total is out of range",
		kind: "invalid_input",
	}
)
