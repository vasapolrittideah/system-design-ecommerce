package client

import (
	"context"
	"errors"

	"github.com/sony/gobreaker/v2"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newBreaker builds the circuit breaker guarding one target service.
//
// One breaker per connection, and one connection per target, so a stock service
// that falls over stops the calls to it without touching the calls to payment.
func newBreaker(cfg Config, log *zap.Logger) *gobreaker.CircuitBreaker[struct{}] {
	return gobreaker.NewCircuitBreaker[struct{}](gobreaker.Settings{
		Name:        cfg.Target,
		MaxRequests: cfg.BreakerHalfOpenRequests,
		Interval:    cfg.BreakerInterval,
		Timeout:     cfg.BreakerTimeout,

		ReadyToTrip: func(counts gobreaker.Counts) bool {
			if counts.Requests < cfg.BreakerMinRequests {
				return false
			}

			return float64(counts.TotalFailures)/float64(counts.Requests) >= cfg.BreakerFailureRatio
		},

		IsSuccessful: func(err error) bool {
			return !unhealthy(err)
		},

		// A caller that gave up is not evidence about the callee. Counting
		// cancellations would let a BFF whose own deadline expired open the
		// breaker on a downstream service that was answering perfectly well.
		IsExcluded: func(err error) bool {
			return status.Code(err) == codes.Canceled
		},

		OnStateChange: func(name string, from, to gobreaker.State) {
			log.Warn("circuit breaker state changed",
				zap.String("target", name),
				zap.String("from", from.String()),
				zap.String("to", to.String()),
			)
		},
	})
}

// unhealthy reports whether err says the target itself is in trouble.
//
// This is the distinction the breaker lives or dies on. NotFound,
// InvalidArgument, and FailedPrecondition are the service working — an order
// that does not exist, a malformed request, a sold-out SKU. Counting those as
// failures means a burst of customers ordering an out-of-stock item trips the
// breaker and takes down the checkout path for everyone.
func unhealthy(err error) bool {
	if err == nil {
		return false
	}

	switch status.Code(err) {
	case codes.Unavailable,
		codes.DeadlineExceeded,
		codes.ResourceExhausted,
		codes.Internal,
		codes.Unknown,
		codes.DataLoss:
		return true
	default:
		return false
	}
}

// breakerUnary fails fast while the target is known to be down.
//
// It sits outside the retry loop, so the breaker observes one outcome per
// logical call rather than one per attempt — otherwise three retries of a
// single failing call would count as three failures and trip it three times as
// fast as configured.
func breakerUnary(cb *gobreaker.CircuitBreaker[struct{}]) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		_, err := cb.Execute(func() (struct{}, error) {
			return struct{}{}, invoker(ctx, method, req, reply, cc, opts...)
		})

		// The breaker's own refusals are library errors, not statuses. They are
		// translated here so the caller's error handling — and errorx's mapping
		// on the way back out to HTTP — sees the same Unavailable it would have
		// seen if the call had gone out and failed.
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return status.Errorf(codes.Unavailable, "circuit breaker open for %s", cb.Name())
		}

		return err
	}
}
