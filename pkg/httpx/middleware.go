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
// This is where the ID enters the system: pkg/grpcx/client reads it off the
// context and forwards it to every service the request fans out to, and those
// services put it on the outbox rows for the events they raise. Skipping this
// middleware breaks that chain at its first link, and one operation shows up in
// the logs as several unrelated ones — with no error anywhere.
//
// It belongs at the top of the chain, so everything after it, a panic included,
// is recorded under the same ID.
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
// chi's own recoverer answers text/plain and prints the stack into the response,
// which hands an attacker the source layout and the frontend a body it cannot
// parse. Here the stack goes to the log under the caller's correlation ID, and
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
