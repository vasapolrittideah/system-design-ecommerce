package client

import (
	"context"
	"math/rand/v2"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// readOnlyPrefixes are the method-name prefixes treated as idempotent without
// being listed in Config.IdempotentMethods.
//
// Method names are what decide retries here, so the hazard is a method that
// reads like a read and is not: a GetOrCreateCart would be retried on the
// strength of its name. Name a mutation for the mutation, or list it explicitly.
var readOnlyPrefixes = []string{"Get", "List", "Batch", "Search", "Count", "Check"}

// retryUnary retries a failed call when retrying is both useful and safe.
//
// Useful means the failure says nothing about whether the request was valid,
// which is Unavailable and a server-reported DeadlineExceeded. Every other code
// is an answer — NotFound stays NotFound however many times it is asked.
//
// Safe means the method can run twice without doing anything twice. Retrying
// PlaceOrder after a timeout of unknown outcome is how a customer gets charged
// twice; that case belongs to an idempotency key at the entry point.
func retryUnary(cfg Config) grpc.UnaryClientInterceptor {
	explicit := make(map[string]struct{}, len(cfg.IdempotentMethods))
	for _, m := range cfg.IdempotentMethods {
		if m = strings.TrimSpace(m); m != "" {
			explicit[m] = struct{}{}
		}
	}

	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		if cfg.MaxAttempts <= 1 || !idempotent(method, explicit) {
			return invoker(ctx, method, req, reply, cc, opts...)
		}

		for attempt := 1; ; attempt++ {
			err := invoker(ctx, method, req, reply, cc, opts...)
			if err == nil || attempt >= cfg.MaxAttempts || !retryable(err) {
				return err
			}

			delay := backoff(attempt, cfg.RetryBackoff, cfg.RetryMaxBackoff)
			if !wait(ctx, delay) {
				return err
			}

			// A failed attempt can still have written into reply — a server
			// that streamed a partial response before failing, say. Clearing it
			// keeps the next attempt from merging into leftovers.
			if msg, ok := reply.(proto.Message); ok {
				proto.Reset(msg)
			}
		}
	}
}

func idempotent(fullMethod string, explicit map[string]struct{}) bool {
	if _, ok := explicit[fullMethod]; ok {
		return true
	}

	name := fullMethod[strings.LastIndex(fullMethod, "/")+1:]
	if _, ok := explicit[name]; ok {
		return true
	}

	for _, prefix := range readOnlyPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}

	return false
}

func retryable(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}

// backoff is exponential with full jitter. The jitter is not decoration: a
// service that comes back up after an outage is met by every caller retrying on
// the same schedule, and the synchronised burst knocks it down again.
func backoff(attempt int, base, max time.Duration) time.Duration {
	// A high enough attempt count overflows the shift into a negative
	// duration, which lands in the same branch as exceeding the cap.
	d := base << (attempt - 1)
	if d <= 0 || d > max {
		d = max
	}
	if d <= 0 {
		return 0
	}

	return rand.N(d)
}

// wait sleeps for d, reporting false if the call ran out of budget instead.
//
// This is what keeps DeadlineExceeded from being retried into the ground. When
// it was our own deadline that expired there is nothing left to retry with, and
// sleeping past it would only replace a real error with a context one.
func wait(ctx context.Context, d time.Duration) bool {
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= d {
		return false
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
