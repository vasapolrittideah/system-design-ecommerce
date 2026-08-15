package server

import (
	"context"
	"runtime/debug"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

// recoveryUnary turns a panic into an Internal status instead of a dead process.
//
// It is outermost so it also catches a panic thrown by any other interceptor.
// The cost of that position is that it runs before logging has put a request
// logger in the context, so panic lines carry trace_id but not correlation_id —
// join them to the access line by trace_id.
//
// The status message is fixed text: whatever the panic value says, it describes
// our internals, and internals do not go over the wire.
func recoveryUnary(log *zap.Logger, m *metrics) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if p := recover(); p != nil {
				resp, err = nil, recovered(ctx, log, m, info.FullMethod, p)
			}
		}()

		return handler(ctx, req)
	}
}

func recoveryStream(log *zap.Logger, m *metrics) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if p := recover(); p != nil {
				err = recovered(stream.Context(), log, m, info.FullMethod, p)
			}
		}()

		return handler(srv, stream)
	}
}

func recovered(ctx context.Context, log *zap.Logger, m *metrics, fullMethod string, p any) error {
	service, method := splitMethod(fullMethod)
	m.panics.WithLabelValues(service, method).Inc()

	// The stack is captured here rather than left to zap's stacktrace option,
	// which would record where the panic was recovered instead of where it was
	// raised — the one frame that matters is already unwound by then.
	logger.From(logger.Into(ctx, log)).Error("panic recovered in gRPC handler",
		zap.String("grpc.service", service),
		zap.String("grpc.method", method),
		zap.Any("panic", p),
		zap.ByteString("stack", debug.Stack()),
	)

	return status.Error(codes.Internal, "internal error")
}
