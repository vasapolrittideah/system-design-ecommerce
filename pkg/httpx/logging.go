package httpx

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

// loggingMiddleware puts the request logger into the context and writes one
// access line per request.
//
// The logger stored is the plain one, never the result of logger.From: From
// derives its trace, correlation, and user fields from the context on every
// call, so storing its output would duplicate those keys on the next hop.
//
// The access line carries no user_id, because authentication is mounted per
// route group and runs inside this middleware — handler logs have it, and
// trace_id joins the two. That is the same trade pkg/grpcx/server makes.
func loggingMiddleware(base *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := logger.Into(r.Context(), base)
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			start := time.Now()
			next.ServeHTTP(wrapped, r.WithContext(ctx))
			elapsed := time.Since(start)

			status := statusOrOK(wrapped.Status())

			logger.From(ctx).Log(levelForStatus(status), "http request",
				zap.String("http.method", r.Method),
				zap.String("http.route", routePattern(r)),
				zap.String("http.path", r.URL.Path),
				zap.Int("http.status", status),
				zap.Int("http.response_bytes", wrapped.BytesWritten()),
				zap.String("peer.address", r.RemoteAddr),
				zap.Duration("duration", elapsed),
			)
		})
	}
}

// levelForStatus keeps expected outcomes out of the error stream.
//
// Every 4xx is a client getting the answer it asked for — a wrong password, a
// stale token, a sold-out SKU — and logging those at error makes the error rate
// a measure of user behaviour rather than of service health, which is how an
// alert on it never stops firing. 503 and 504 are warnings because they usually
// mean a dependency is struggling rather than that this service is broken; only
// the rest of the 5xx range says the fault is here.
func levelForStatus(status int) zapcore.Level {
	switch {
	case status < http.StatusInternalServerError:
		return zapcore.InfoLevel

	case status == http.StatusServiceUnavailable, status == http.StatusGatewayTimeout:
		return zapcore.WarnLevel

	default:
		return zapcore.ErrorLevel
	}
}
