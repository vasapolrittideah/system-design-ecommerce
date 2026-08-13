package client

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

// deadlineUnary applies a default deadline to a call that arrived without one.
//
// A gRPC call with no deadline waits forever, and forever is long enough for
// the caller's own goroutines, connections, and callers to pile up behind it.
// A caller that set its own budget keeps it: this only fills the gap, and never
// extends an existing deadline.
func deadlineUnary(timeout time.Duration) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		if _, ok := ctx.Deadline(); ok || timeout <= 0 {
			return invoker(ctx, method, req, reply, cc, opts...)
		}

		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// propagateUnary forwards the correlation ID and the caller identity to the
// next hop.
//
// The trace itself is carried by the otel stats handler, which injects
// traceparent on the wire. This covers what traceparent does not: the
// correlation ID that ties an entire user-visible operation together across
// gRPC calls and the Kafka events they cause, and the identity, so a service
// three hops in can still answer "whose order is this".
func propagateUnary() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		return invoker(propagate(ctx), method, req, reply, cc, opts...)
	}
}

func propagateStream() grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		return streamer(propagate(ctx), desc, cc, method, opts...)
	}
}

func propagate(ctx context.Context) context.Context {
	pairs := make([]string, 0, 6)

	if id := logger.CorrelationID(ctx); id != "" {
		pairs = append(pairs, grpcx.MetadataCorrelationID, id)
	}
	if id, ok := grpcx.IdentityFrom(ctx); ok {
		if id.UserID != "" {
			pairs = append(pairs, grpcx.MetadataUserID, id.UserID)
		}
		if len(id.Roles) > 0 {
			pairs = append(pairs, grpcx.MetadataUserRoles, strings.Join(id.Roles, ","))
		}
	}

	if len(pairs) == 0 {
		return ctx
	}

	return metadata.AppendToOutgoingContext(ctx, pairs...)
}
