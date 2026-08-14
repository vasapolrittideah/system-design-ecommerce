package httpx

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

// CorrelationID adopts the caller's correlation ID or mints one, puts it in the
// request context, and echoes it back as a response header.
//
// This is where the ID that ties a user-visible operation together enters the
// system. Kong stamps it on the way in; from here pkg/grpcx/client reads it off
// the context and forwards it to every service the request fans out to, and
// those services put it on the outbox rows for the Kafka events they raise. A
// BFF that skips this middleware breaks that chain at its first link: the
// context has no ID to forward, every downstream service mints its own, and one
// operation shows up in the logs as a handful of unrelated ones.
//
// It is chi-compatible, and belongs at the top of the chain so that everything
// after it — including a panic — is recorded under the same ID.
func CorrelationID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(grpcx.MetadataCorrelationID)
		if id == "" {
			id = uuid.NewString()
		}

		w.Header().Set(grpcx.MetadataCorrelationID, id)

		next.ServeHTTP(w, r.WithContext(logger.WithCorrelationID(r.Context(), id)))
	})
}

// Recover turns a panic into the same error body every other failure produces.
//
// chi's own recoverer answers text/plain and re-prints the stack into the
// response, which hands an attacker the source layout and hands the frontend a
// body it cannot parse — the one response shape it is allowed not to handle.
// The stack goes to the log, under the correlation ID the caller was given, and
// the client gets an ordinary 500.
//
// http.ErrAbortHandler is re-panicked untouched: it is the standard library's
// way of saying a handler deliberately dropped the connection, and swallowing
// it would answer a request nobody is listening to.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if err, ok := recovered.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(recovered)
			}

			logger.From(r.Context()).Error("recovered from panic",
				zap.Any("panic", recovered),
				zap.Stack("stack"),
			)

			WriteError(w, r, errorx.New(errorx.KindInternal, "panic: %v", recovered))
		}()

		next.ServeHTTP(w, r)
	})
}

// NotFound answers an unrouted path in this API's error shape rather than
// chi's plain-text default, so a client never has to parse two formats.
func NotFound(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, errorx.New(errorx.KindNotFound, "no route for %s %s", r.Method, r.URL.Path).
		WithReason("ROUTE_NOT_FOUND"))
}

// MethodNotAllowed answers a known path called with the wrong method.
//
// It maps to 405 through [Statuser] because gRPC has no code for it — the
// closest, Unimplemented, would come out as 501 and tell the caller the feature
// does not exist rather than that they used the wrong verb.
func MethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, withStatus(http.StatusMethodNotAllowed,
		errorx.New(errorx.KindInvalidInput, "%s is not allowed on %s", r.Method, r.URL.Path).
			WithReason("METHOD_NOT_ALLOWED")))
}
