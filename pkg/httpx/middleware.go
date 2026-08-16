package httpx

import (
	"context"
	"errors"
	"net/http"
	"time"

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
	return recoverWith(nil)(next)
}

// recoverWith is [Recover] with somewhere to count what it caught. A nil
// serverMetrics is the plain behaviour, which is what a router built without a
// registry gets.
func recoverWith(m *serverMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				if err, ok := recovered.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(recovered)
				}

				if m != nil {
					m.panics.WithLabelValues(r.Method, routePattern(r)).Inc()
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
}

// Timeout gives every request below it a context deadline.
//
// This is the BFF's half of the cascading budget — Kong 5s, BFF 800ms,
// downstream 300ms — and it works by deadline propagation alone: pkg/grpcx/client
// inherits the deadline from the context it is handed, so one setting here bounds
// every fan-out call the request makes without any handler passing it along.
//
// It deliberately does not use http.TimeoutHandler, which writes its own
// text/plain 503 over whatever the handler was producing. That is a third
// response shape the frontend would have to parse, and it fires while the
// handler is still running. Here an expired budget surfaces as a
// DeadlineExceeded from whichever call was in flight, which pkg/errorx already
// maps to 504 with a reason code — so the timeout answers in the same shape as
// everything else.
//
// The trade is that a handler which ignores its context is not interrupted by
// this. [ServerConfig.WriteTimeout] is the backstop for that case.
func Timeout(budget time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), budget)
			defer cancel()

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
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
