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

// The refusals the product state machine can produce. They are values rather
// than types because a caller has to branch on them with errors.Is, and because
// the boundary turns each one into its own reason code — the string a client
// actually writes an if against.
//
// All but one are conflicts: the request was well-formed and the aggregate is
// simply not in a state where it can be honoured, which is 409 and not 400. A
// client that retries them unchanged gets the same answer.
var (
	// ErrProductArchived is the answer to every write against an archived
	// product. Archiving is one-way, so this is permanent rather than a state
	// the caller can wait out — a product that should sell again is a new one.
	ErrProductArchived = &sentinelError{
		msg:  "product is archived",
		kind: "conflict",
	}

	// ErrProductHasNoVariants blocks publishing a product nobody can buy. A
	// draft is allowed to exist before anyone has decided what it costs, and
	// this is where that stops being allowed.
	ErrProductHasNoVariants = &sentinelError{
		msg:  "product has no variants to sell",
		kind: "conflict",
	}

	// ErrDuplicateSKU is a SKU already used by another variant of the same
	// product. Uniqueness across the whole service is the UNIQUE constraint's
	// to enforce, since no aggregate can see the rows it would have to check;
	// this catches the half the aggregate can see, where the answer names the
	// conflict instead of arriving as a constraint violation from pgx.
	ErrDuplicateSKU = &sentinelError{
		msg:  "sku is already used by another variant of this product",
		kind: "conflict",
	}

	// ErrCurrencyMismatch is a price in a currency the product's other variants
	// are not sold in. A cart summing two currencies produces a number that is
	// not a price in either of them, and the sum is computed far from here.
	ErrCurrencyMismatch = &sentinelError{
		msg:  "variant price is in a different currency to the rest of the product",
		kind: "conflict",
	}

	// ErrVariantNotFound is an id that belongs to no variant of this product —
	// including one that exists under a different product, which the aggregate
	// cannot see and must not appear to have modified.
	ErrVariantNotFound = &sentinelError{
		msg:  "variant does not belong to this product",
		kind: "not_found",
	}
)
