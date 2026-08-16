package httpx

import (
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

// RouterOption customizes the middleware chain beyond its defaults.
type RouterOption func(*routerOptions)

type routerOptions struct {
	logger     *zap.Logger
	registerer prometheus.Registerer
	timeout    time.Duration
}

// WithRouterLogger sets the logger the access line is written through and the
// one installed into every request context. Without it the chain falls back to
// zap's global, which is a no-op until logger.SetGlobal has been called.
func WithRouterLogger(log *zap.Logger) RouterOption {
	return func(o *routerOptions) {
		o.logger = log
	}
}

// WithRouterMetrics publishes the http_server_* set to reg.
//
// The registry is passed explicitly rather than reached for, because
// pkg/observability owns it — nothing in this repo touches
// prometheus.DefaultRegisterer.
func WithRouterMetrics(reg prometheus.Registerer) RouterOption {
	return func(o *routerOptions) {
		o.registerer = reg
	}
}

// WithRequestTimeout applies a per-request budget. See [Timeout] for what that
// bounds and what it deliberately does not.
func WithRequestTimeout(budget time.Duration) RouterOption {
	return func(o *routerOptions) {
		o.timeout = budget
	}
}

// MustNewRouter builds the chi router a BFF mounts its endpoints on, with the
// middleware every request needs already attached and both of chi's fallback
// handlers replaced.
//
// The chain, outermost first:
//
//  1. [CorrelationID] — so everything after it, a panic included, is logged
//     under the ID the caller was handed back.
//  2. access logging — installs the request logger, writes one line per
//     request.
//  3. metrics — the http_server_* set, when a registry was given.
//  4. [Recover] — so a panic in a handler becomes a 500 in this API's error
//     shape rather than chi's plain-text stack dump.
//  5. [Validator.Localize] — so validation failures come back in the language
//     the caller asked for.
//  6. [Timeout] — the BFF's share of the cascading budget, when one was given.
//
// Recovery sits inside logging and metrics rather than outside them, which is
// the reverse of pkg/grpcx/server. There the ID is minted by the logging
// interceptor, so recovery has to be outermost to catch a panic thrown above it.
// Here the ID is a header the caller already sent, and putting recovery inside
// buys something that ordering cannot: a panicked request is logged and measured
// as the 500 it was turned into, instead of unwinding past both with no status
// at all.
//
// It panics only on duplicate metric registration, which is a wiring mistake
// with no runtime remedy — the same reason [MustNewValidator] panics.
//
// Everything else is left to the caller: routes, authentication, and CORS belong
// to the service mounting this, and Kong already handles the edge concerns.
func MustNewRouter(v *Validator, opts ...RouterOption) *chi.Mux {
	o := routerOptions{logger: zap.L()}
	for _, opt := range opts {
		opt(&o)
	}

	var metrics *serverMetrics
	if o.registerer != nil {
		built, err := newServerMetrics(o.registerer)
		if err != nil {
			panic(err)
		}

		metrics = built
	}

	r := chi.NewRouter()

	r.Use(CorrelationID)
	r.Use(loggingMiddleware(o.logger))
	if metrics != nil {
		r.Use(metricsMiddleware(metrics))
	}
	r.Use(recoverWith(metrics))
	r.Use(v.Localize)
	if o.timeout > 0 {
		r.Use(Timeout(o.timeout))
	}

	r.NotFound(NotFound)
	r.MethodNotAllowed(MethodNotAllowed)

	return r
}
