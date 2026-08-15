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

	// Message says what is wrong with it, and never repeats the value: an email
	// address is the user's, and this string reaches a log line.
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

// The failures the sign-in flow can produce. They are values rather than types
// because the use case has to branch on them — turning a missing user into
// invalid credentials, turning a spent token into a revoked family — and
// errors.Is against a value is how that branch is written.
//
// All three are Unauthenticated: the credential is the problem, and the client
// should obtain a new one. The distinctions below are for this service; which
// reason code each becomes is decided at the boundary, where two of them
// deliberately collapse into one answer.
var (
	// ErrInvalidCredentials is the answer to a wrong password and to an email
	// that was never registered, and must stay a single value for exactly that
	// reason: two answers are a way to ask which addresses have accounts.
	ErrInvalidCredentials = &sentinelError{
		msg:  "invalid credentials",
		kind: "unauthenticated",
	}

	// ErrRefreshTokenExpired is a chain that reached its fixed end. There is
	// nothing left to refresh with, so the client signs in again — which is why
	// this cannot share a reason code with an expired access token.
	ErrRefreshTokenExpired = &sentinelError{
		msg:  "refresh token has expired",
		kind: "unauthenticated",
	}

	// ErrRefreshTokenRevoked is a token spent by a rotation, revoked by a
	// logout, or caught in a family revocation.
	//
	// The wire cannot tell which, deliberately: telling a caller "that one was
	// already used" tells a thief their copy was the real one.
	ErrRefreshTokenRevoked = &sentinelError{
		msg:  "refresh token is no longer valid",
		kind: "unauthenticated",
	}
)
