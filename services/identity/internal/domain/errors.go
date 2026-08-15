package domain

import "fmt"

// ValidationError reports a value this package refused.
//
// It declares its kind structurally — ErrorKind returns one of the strings
// pkg/errorx knows, and a method signature is not an import — which is what
// lets errorx.ToGRPC answer InvalidArgument for it while this package still
// depends on nothing but the standard library.
//
// The kinds are transport-shaped on purpose: "invalid_input", never
// "ErrEmailMalformed". What the failure means in this service's vocabulary is
// carried by Field and Message, and by the reason code an outer layer attaches.
type ValidationError struct {
	// Field is the name the caller used, so a client can point at the input it
	// got wrong. It is the proto field name where there is one.
	Field string

	// Message says what is wrong with it, and never repeats the value: an
	// email address is the user's, and this string reaches a log line.
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ErrorKind implements the structural contract pkg/errorx resolves errors
// through. See pkg/errorx.Kinder for why it is a method rather than an import.
func (e ValidationError) ErrorKind() string { return "invalid_input" }

// sentinelError is a failure that is the same every time it happens, so there
// is nothing per-occurrence to carry and one package-level value will do.
//
// It declares its kind the same way ValidationError does. A sentinel built with
// errors.New instead would declare none, which resolves to Internal — the
// caller would be told the service is broken, and ToGRPC would scrub the
// message that said otherwise.
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
// should obtain a new one rather than be told it may not do this. The finer
// distinctions below are for this service, not for the client; the reason code
// each becomes is decided at the boundary, where two of them deliberately
// collapse into one answer.
var (
	// ErrInvalidCredentials is the answer to a wrong password and to an email
	// that was never registered, and it must stay a single value for exactly
	// that reason. Two errors here become two answers, and two answers are a
	// way to ask which addresses have accounts.
	ErrInvalidCredentials = &sentinelError{
		msg:  "invalid credentials",
		kind: "unauthenticated",
	}

	// ErrRefreshTokenExpired is a chain that reached its fixed end. The client
	// signs in again — unlike an expired access token, there is nothing left to
	// refresh with, which is why this cannot share a reason code with one.
	ErrRefreshTokenExpired = &sentinelError{
		msg:  "refresh token has expired",
		kind: "unauthenticated",
	}

	// ErrRefreshTokenRevoked is a token that was already spent by a rotation,
	// or revoked by a logout, or caught up in a family revocation.
	//
	// Being spent is the interesting case: it means two parties hold the same
	// token, so the service revokes the whole chain. That reaction is the
	// service's, though — on the wire this is indistinguishable from any other
	// unusable token, because telling a caller "that one was already used"
	// tells a thief their copy was the real one.
	ErrRefreshTokenRevoked = &sentinelError{
		msg:  "refresh token is no longer valid",
		kind: "unauthenticated",
	}
)
