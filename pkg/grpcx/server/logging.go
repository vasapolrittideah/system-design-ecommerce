package server

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

// loggingUnary puts the request logger and the correlation ID into the context,
// then writes one access line per call.
//
// The correlation ID is adopted from the caller when there is one and minted
// when there is not, so the ID that Kong stamped on an inbound HTTP request
// survives every gRPC hop and every Kafka event it eventually causes. It also
// goes back out as a response header, which is what lets a caller quote it in a
// bug report.
//
// The access line does not carry user_id: the auth interceptor runs inside this
// one, so the identity it resolves is not in the context this line is written
// from. Handler logs have it, and trace_id joins the two.
func loggingUnary(base *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = requestContext(ctx, base)

		start := time.Now()
		resp, err := handler(ctx, req)
		logCall(ctx, info.FullMethod, typeUnary, err, time.Since(start))

		return resp, err
	}
}

func loggingStream(base *zap.Logger) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := requestContext(stream.Context(), base)

		start := time.Now()
		err := handler(srv, withContext(stream, ctx))
		logCall(ctx, info.FullMethod, streamType(info), err, time.Since(start))

		return err
	}
}

// requestContext installs the plain logger and the correlation ID.
//
// The logger stored is the base one, never the result of logger.From — From
// derives its trace, correlation, and user fields from the context on every
// call, so storing its output would duplicate those keys on the next hop.
func requestContext(ctx context.Context, base *zap.Logger) context.Context {
	id := correlationID(ctx)

	// Best effort: the header is already gone if the handler streamed a
	// response before this ran, and a missing echo is not worth failing a call.
	_ = grpc.SetHeader(ctx, metadata.Pairs(grpcx.MetadataCorrelationID, id))

	return logger.WithCorrelationID(logger.Into(ctx, base), id)
}

func correlationID(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if values := md.Get(grpcx.MetadataCorrelationID); len(values) > 0 && values[0] != "" {
			return values[0]
		}
	}

	return uuid.NewString()
}

func logCall(ctx context.Context, fullMethod, callType string, err error, elapsed time.Duration) {
	service, method := splitMethod(fullMethod)
	code := status.Code(err)

	fields := []zap.Field{
		zap.String("grpc.service", service),
		zap.String("grpc.method", method),
		zap.String("grpc.type", callType),
		zap.String("grpc.code", code.String()),
		zap.Duration("duration", elapsed),
	}
	if p, ok := peer.FromContext(ctx); ok {
		fields = append(fields, zap.String("peer.address", p.Addr.String()))
	}
	if err != nil {
		fields = append(fields, zap.Error(err))
	}

	logger.From(ctx).Log(levelForCode(code), "gRPC call", fields...)
}

// levelForCode keeps expected outcomes out of the error stream.
//
// A NotFound or a FailedPrecondition is the system working: someone asked for
// an order that does not exist, or tried to pay one twice. Logging those at
// error makes the error rate a measure of user behaviour rather than of service
// health, and an alert on it fires forever. Only codes that mean "this service
// is broken" get error level.
func levelForCode(code codes.Code) zapcore.Level {
	switch code {
	case codes.OK:
		return zapcore.InfoLevel

	case codes.NotFound,
		codes.AlreadyExists,
		codes.InvalidArgument,
		codes.FailedPrecondition,
		codes.OutOfRange,
		codes.Canceled,
		codes.Unauthenticated,
		codes.PermissionDenied:
		return zapcore.InfoLevel

	case codes.DeadlineExceeded,
		codes.ResourceExhausted,
		codes.Aborted,
		codes.Unavailable:
		return zapcore.WarnLevel

	default:
		return zapcore.ErrorLevel
	}
}
