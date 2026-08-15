package errorx

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// codeForKind is the mapping the whole system agrees on.
//
// Anything finer-grained than these six — which precondition failed, which
// resource was missing — is the reason code's job, not the status code's.
var codeForKind = map[Kind]codes.Code{
	KindNotFound:        codes.NotFound,
	KindInvalidInput:    codes.InvalidArgument,
	KindConflict:        codes.FailedPrecondition,
	KindUnauthenticated: codes.Unauthenticated,
	KindUnauthorized:    codes.PermissionDenied,
	KindInternal:        codes.Internal,
}

// ToGRPC turns any error a handler holds into the status its caller should see.
//
// It resolves in a fixed order, and each step exists because the one after it
// would get that case wrong:
//
//  1. A declared kind — from [Error] or from a domain error implementing
//     [Kinder] — becomes its mapped code. This is the ordinary path.
//  2. An error that is already a gRPC status keeps its code. A gateway calling
//     a downstream service holds one of these, and flattening a downstream
//     Unavailable into Internal would hide from the caller's circuit breaker
//     exactly what it exists to detect.
//  3. A cancelled or expired context becomes Canceled or DeadlineExceeded.
//     These arrive bare from pgx and from anything selecting on ctx.Done, and
//     reporting a caller who hung up as Internal would put client behaviour into
//     the error rate and into breakers that should not have been touched.
//  4. Anything else becomes Internal with a fixed message.
//
// Only the Internal message is replaced; every other kind is a fact the caller
// asked for. The returned error still wraps the original, so the access log
// records the full chain while the client receives only the status.
func ToGRPC(err error) error {
	if err == nil {
		return nil
	}

	if kind, ok := declaredKind(err); ok {
		return statusError(statusFor(kind, err), err)
	}

	if st, ok := status.FromError(err); ok {
		return statusError(st, err)
	}

	switch {
	case errors.Is(err, context.Canceled):
		return statusError(status.New(codes.Canceled, "request cancelled"), err)
	case errors.Is(err, context.DeadlineExceeded):
		return statusError(status.New(codes.DeadlineExceeded, "request timed out"), err)
	}

	return statusError(statusFor(KindInternal, err), err)
}

// statusFor builds the status a classified error is reported as, carrying the
// ErrorInfo detail that lets a client handle one specific failure without
// parsing the message.
func statusFor(kind Kind, err error) *status.Status {
	message := err.Error()
	if kind == KindInternal {
		message = "internal error"
	}

	st := status.New(codeForKind[kind], message)

	detailed, detailErr := st.WithDetails(&errdetails.ErrorInfo{
		Reason:   Reason(err),
		Metadata: Metadata(err),
	})
	if detailErr != nil {
		// Only marshalling can fail here, and a status without its detail is
		// still a correct answer — worth keeping over failing the call.
		return st
	}

	return detailed
}

// wireReason returns the reason code an incoming gRPC status reports, and
// whether err was a status at all.
//
// A status raised below the handlers — Unavailable from a tripped breaker,
// Unimplemented from a version skew — carries no ErrorInfo, so the code name
// stands in. Falling back to INTERNAL there would have the Composition API
// answer 503 while naming the failure an internal error.
func wireReason(err error) (string, bool) {
	st, ok := status.FromError(err)
	if !ok {
		return "", false
	}

	if info, ok := errorInfo(err); ok && info.GetReason() != "" {
		return info.GetReason(), true
	}

	return screamingSnake(st.Code().String()), true
}

// screamingSnake turns a gRPC code's CamelCase name into the SCREAMING_SNAKE
// shape reason codes are written in, so a reason read off the wire looks like
// one a service set by hand: DeadlineExceeded becomes DEADLINE_EXCEEDED.
func screamingSnake(name string) string {
	var b strings.Builder
	b.Grow(len(name) + 4)

	for i, r := range name {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToUpper(r))
	}

	return b.String()
}

// errorInfo returns the ErrorInfo detail on err's gRPC status, if it has one.
// It is how a caller — the Composition API, mostly — reads back the reason and
// metadata a service attached on the other side of the wire.
func errorInfo(err error) (*errdetails.ErrorInfo, bool) {
	st, ok := status.FromError(err)
	if !ok {
		return nil, false
	}

	for _, detail := range st.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			return info, true
		}
	}

	return nil, false
}

// grpcError reports one thing to the client and another to the log: grpc-go
// serialises it through GRPCStatus, while Error and Unwrap keep the original
// chain for the logging interceptor and for errors.Is in a test.
//
// Without it, scrubbing an Internal message would also erase the only record of
// what broke.
type grpcError struct {
	st    *status.Status
	cause error
}

func statusError(st *status.Status, cause error) error {
	return &grpcError{st: st, cause: cause}
}

func (e *grpcError) Error() string { return e.cause.Error() }

func (e *grpcError) GRPCStatus() *status.Status { return e.st }

func (e *grpcError) Unwrap() error { return e.cause }
