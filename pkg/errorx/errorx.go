// Package errorx is the one place an error changes vocabulary.
//
// An error is born inside a domain package as a business fact — this order does
// not exist, this transition is not allowed — and has to leave the process as a
// gRPC status code, and sometimes as an HTTP status after that. Doing the
// translation in each handler produces a different answer in each handler, so
// it happens here and nowhere else:
//
//	func (h *Handler) GetOrder(ctx context.Context, req *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
//		order, err := h.svc.Get(ctx, req.GetOrderId())
//		if err != nil {
//			return nil, errorx.ToGRPC(err)
//		}
//
//		return &orderv1.GetOrderResponse{Order: toProto(order)}, nil
//	}
//
// # How an error declares what it means
//
// Every error resolves to exactly one [Kind], and there are two ways to say so.
//
// Code outside a domain package — repositories translating pgx, gateways
// translating a remote status, use cases raising an orchestration failure —
// builds one directly:
//
//	if errors.Is(err, pgx.ErrNoRows) {
//		return nil, errorx.New(errorx.KindNotFound, "order %s not found", id)
//	}
//
// A domain package cannot do that: internal/domain imports only the standard
// library, so it cannot import this package either. It declares its kind
// structurally instead, by implementing [Kinder] — one method returning one of
// the [Kind] strings, no imports involved:
//
//	// internal/domain/errors.go
//	package domain
//
//	type Error struct {
//		kind string
//		msg  string
//	}
//
//	func (e *Error) Error() string     { return e.msg }
//	func (e *Error) ErrorKind() string { return e.kind }
//
//	var (
//		ErrNotFound          = &Error{kind: "not_found", msg: "order not found"}
//		ErrInvalidTransition = &Error{kind: "conflict", msg: "invalid status transition"}
//		ErrOutOfStock        = &Error{kind: "conflict", msg: "out of stock"}
//	)
//
// This is the same trick the standard library plays with Unwrap and Stringer:
// the contract is a method signature, so the dependency points at nothing. A
// service's own vocabulary — out of stock, already refunded, cart expired —
// stays in that service, and this package stays free of business concepts while
// still mapping them correctly.
//
// An error that declares no kind at all maps to Internal. That is deliberate:
// an unrecognised failure is a bug until someone classifies it, and defaulting
// the other way would hand clients a 400 for a broken database.
//
// # Reasons and metadata
//
// A status code says how the caller should react; it does not say what
// happened. A client that needs to tell "sold out" apart from "order already
// paid" — both FailedPrecondition, both 409 — reads the machine-stable reason
// and its metadata, which travel as an ErrorInfo detail on the status:
//
//	return nil, errorx.New(errorx.KindConflict, "sku %s is sold out", sku).
//		WithReason("OUT_OF_STOCK").
//		WithMetadata(map[string]string{"sku": sku})
//
// Without an explicit reason the kind supplies a default one, so every error
// leaving a service carries something a client can branch on rather than a
// message it would have to pattern-match.
package errorx

import (
	"errors"
	"fmt"
	"maps"
)

// Kind is the closed set of meanings this package translates.
//
// The values are deliberately transport-shaped rather than business-shaped:
// "conflict", not "out of stock". A service names its own failures in its own
// domain package and points them at one of these.
type Kind string

const (
	// KindNotFound is a resource the caller named that does not exist.
	KindNotFound Kind = "not_found"

	// KindInvalidInput is a request that is malformed or self-contradictory,
	// independent of any state. Field-level shape checks belong in the proto
	// under protovalidate; this is for what the interceptor cannot express.
	KindInvalidInput Kind = "invalid_input"

	// KindConflict is a request that is well-formed but disagrees with the
	// current state: an invalid state transition, a sold-out SKU, an expired
	// reservation. Retrying it unchanged will fail the same way until the state
	// changes.
	KindConflict Kind = "conflict"

	// KindUnauthorized is an authenticated caller reaching for something that
	// is not theirs. Whether the caller is authenticated at all is settled at
	// the edge, long before an error of this kind is possible.
	KindUnauthorized Kind = "unauthorized"

	// KindInternal is everything else — the failures no client can act on.
	// Its message never leaves the process.
	KindInternal Kind = "internal"
)

// Kinder is implemented by an error that names its own kind.
//
// It exists so a domain package can classify its errors without importing
// anything: the method returns one of the [Kind] values as a plain string. A
// value outside that set is treated as unclassified, and therefore Internal.
//
// Domain code never references this interface by name — it only has to have the
// method. The declaration is here so there is one place that documents the
// contract.
type Kinder interface {
	ErrorKind() string
}

// reasons are the default ErrorInfo reason codes, used when the caller does not
// set a more specific one.
var reasons = map[Kind]string{
	KindNotFound:     "NOT_FOUND",
	KindInvalidInput: "INVALID_INPUT",
	KindConflict:     "CONFLICT",
	KindUnauthorized: "UNAUTHORIZED",
	KindInternal:     "INTERNAL",
}

// Sentinels for the code that only needs to ask "what kind of failure is this",
// via errors.Is. They carry no reason or metadata of their own; New builds the
// errors that do.
var (
	ErrNotFound     = &Error{kind: KindNotFound, msg: "not found"}
	ErrInvalidInput = &Error{kind: KindInvalidInput, msg: "invalid input"}
	ErrConflict     = &Error{kind: KindConflict, msg: "conflict"}
	ErrUnauthorized = &Error{kind: KindUnauthorized, msg: "unauthorized"}
	ErrInternal     = &Error{kind: KindInternal, msg: "internal error"}
)

// Error is a classified error: a kind, a human message, and the machine-
// readable reason and metadata that reach the client as an ErrorInfo detail.
//
// The zero value is not useful; build one with [New] or [Wrap].
type Error struct {
	kind   Kind
	msg    string
	reason string
	meta   map[string]string
	cause  error
}

// New builds an error of kind with a formatted message.
func New(kind Kind, format string, args ...any) *Error {
	return &Error{kind: kind, msg: fmt.Sprintf(format, args...)}
}

// Wrap classifies an existing error, keeping it reachable through errors.Is and
// errors.As. Use it where a lower layer's error is the explanation — a pgx
// failure behind a repository, a remote status behind a gateway.
//
// Wrap(nil, ...) returns nil, so a call site can hand it the result of the
// operation it is describing without checking first.
func Wrap(err error, kind Kind, format string, args ...any) *Error {
	if err == nil {
		return nil
	}

	return &Error{kind: kind, msg: fmt.Sprintf(format, args...), cause: err}
}

// WithReason returns a copy carrying a machine-stable reason code, by
// convention SCREAMING_SNAKE_CASE such as "OUT_OF_STOCK". It is what a client
// branches on, so it is part of the API and changing one is a breaking change.
func (e *Error) WithReason(reason string) *Error {
	clone := e.clone()
	clone.reason = reason

	return clone
}

// WithMetadata returns a copy with md merged into any metadata already set —
// the facts a client needs to act on the failure, such as the SKU that was
// short or the status the aggregate was actually in.
//
// Keys and values reach the client, so nothing secret goes in here.
func (e *Error) WithMetadata(md map[string]string) *Error {
	clone := e.clone()
	if clone.meta == nil {
		clone.meta = make(map[string]string, len(md))
	}
	maps.Copy(clone.meta, md)

	return clone
}

// clone copies e deeply enough that the copy cannot write through to the
// original. Without this the package-level sentinels would accumulate whatever
// reason and metadata the last caller attached to them.
func (e *Error) clone() *Error {
	copied := *e
	copied.meta = maps.Clone(e.meta)

	return &copied
}

func (e *Error) Error() string {
	switch {
	case e.cause == nil:
		return e.msg
	case e.msg == "":
		return e.cause.Error()
	default:
		return e.msg + ": " + e.cause.Error()
	}
}

// Unwrap returns the error this one was built around, if any.
func (e *Error) Unwrap() error { return e.cause }

// ErrorKind implements [Kinder], so an *Error and a domain error are resolved
// by the same lookup rather than by two branches that can drift apart.
func (e *Error) ErrorKind() string { return string(e.kind) }

// Is reports whether target is an *Error of the same kind, which is what makes
// errors.Is(err, errorx.ErrNotFound) work for any error of that kind.
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)

	return ok && other.kind == e.kind
}

// KindOf returns the kind err resolves to, and Internal for anything that
// declares none. It returns the empty Kind only for a nil error.
func KindOf(err error) Kind {
	if err == nil {
		return ""
	}

	if kind, ok := declaredKind(err); ok {
		return kind
	}

	return KindInternal
}

// declaredKind returns the kind err declares and whether it declared one at
// all. The distinction matters to ToGRPC, which has somewhere better to look —
// an already-formed gRPC status, a cancelled context — before falling back to
// Internal.
//
// One errors.As walk finds both an *Error and a domain error, because *Error
// implements Kinder too, so the outermost declaration in the chain wins rather
// than whichever type happened to be searched for first.
func declaredKind(err error) (Kind, bool) {
	var kinder Kinder
	if !errors.As(err, &kinder) {
		return "", false
	}

	kind := Kind(kinder.ErrorKind())
	if _, known := reasons[kind]; !known {
		return "", false
	}

	return kind, true
}

// Reason returns the machine-stable reason code for err, resolved in the same
// order the rest of the package resolves everything: what the error says about
// itself first, what the wire said second.
//
//  1. The reason set with [Error.WithReason].
//  2. The default for a declared kind. This outranks anything found on the wire
//     because an error that was reclassified on the way out must not keep
//     announcing the reason of the failure it was built from — a downstream
//     NotFound rewrapped as invalid input answers INVALID_INPUT, not NOT_FOUND.
//  3. The reason on an incoming status, which is how a caller reads back what a
//     service attached on the other side of the wire.
//
// It returns "" for a nil error.
func Reason(err error) string {
	if err == nil {
		return ""
	}

	var e *Error
	if errors.As(err, &e) && e.reason != "" {
		return e.reason
	}

	if kind, ok := declaredKind(err); ok {
		return reasons[kind]
	}

	if reason, ok := wireReason(err); ok {
		return reason
	}

	return reasons[KindInternal]
}

// Metadata returns the facts attached to err — set with [Error.WithMetadata] or
// carried on the ErrorInfo detail of an incoming gRPC status — and nil when
// there are none.
//
// It resolves in the same order as [Reason], so metadata and reason always
// describe the same failure rather than the reason describing the outer error
// and the metadata describing the one it wrapped.
func Metadata(err error) map[string]string {
	if err == nil {
		return nil
	}

	var e *Error
	if errors.As(err, &e) && len(e.meta) > 0 {
		return maps.Clone(e.meta)
	}

	if _, declared := declaredKind(err); declared {
		return nil
	}

	if info, ok := errorInfo(err); ok && len(info.GetMetadata()) > 0 {
		return maps.Clone(info.GetMetadata())
	}

	return nil
}
