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
	// ErrPaymentNotFound is an id nobody has. It is also the answer to reading
	// somebody else's payment: telling a caller that an attempt exists but is
	// not theirs is telling them something about another customer.
	ErrPaymentNotFound = &sentinelError{
		msg:  "payment does not exist",
		kind: "not_found",
	}

	// ErrPaymentSucceeded refuses failing an attempt whose money is already in.
	// A charge that is reversed after it settled is a refund or a chargeback —
	// a second movement of money with a provider on the other end of it — and
	// not a state this aggregate may reach by being told the first one failed.
	ErrPaymentSucceeded = &sentinelError{
		msg:  "payment already succeeded",
		kind: "conflict",
	}

	// ErrPaymentFailed refuses settling an attempt that is already over. A
	// provider reporting success for a charge it declined is a contradiction
	// rather than a late answer, and taking the money on the strength of it is
	// the outcome nobody can undo cheaply.
	ErrPaymentFailed = &sentinelError{
		msg:  "payment already failed",
		kind: "conflict",
	}

	// ErrProviderReferenceConflict refuses replacing the provider's identifier
	// for an attempt with a different one. Two references mean two charges, and
	// this aggregate can only ever describe one of them.
	ErrProviderReferenceConflict = &sentinelError{
		msg:  "payment already names a different provider reference",
		kind: "conflict",
	}
)
