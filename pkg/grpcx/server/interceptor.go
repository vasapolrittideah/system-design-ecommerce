package server

import (
	"context"
	"strings"

	"google.golang.org/grpc"
)

// gRPC call kinds, used as the grpc_type metric label so a slow bidi stream
// cannot be mistaken for slow unary traffic on the same dashboard.
const (
	typeUnary        = "unary"
	typeClientStream = "client_stream"
	typeServerStream = "server_stream"
	typeBidiStream   = "bidi_stream"
)

// splitMethod breaks "/ecommerce.order.v1.OrderService/GetOrder" into its
// service and method halves, which is the shape both the metric labels and the
// log fields want.
func splitMethod(fullMethod string) (service, method string) {
	trimmed := strings.TrimPrefix(fullMethod, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[:i], trimmed[i+1:]
	}

	return "unknown", trimmed
}

func streamType(info *grpc.StreamServerInfo) string {
	switch {
	case info.IsClientStream && info.IsServerStream:
		return typeBidiStream
	case info.IsClientStream:
		return typeClientStream
	default:
		return typeServerStream
	}
}

// wrappedStream carries a replacement context down to the handler. A
// grpc.ServerStream hands its context out but offers no way to enrich it, so an
// interceptor that wants to add anything to the stream's context has to wrap
// the stream itself.
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *wrappedStream) Context() context.Context {
	return s.ctx
}

// withContext wraps stream so the handler sees ctx, flattening nested wrappers
// so a chain of five interceptors does not build five layers of indirection.
func withContext(ctx context.Context, stream grpc.ServerStream) grpc.ServerStream {
	if w, ok := stream.(*wrappedStream); ok {
		return &wrappedStream{ServerStream: w.ServerStream, ctx: ctx}
	}

	return &wrappedStream{ServerStream: stream, ctx: ctx}
}
