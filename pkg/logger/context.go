package logger

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type contextKey int

const (
	loggerKey contextKey = iota
	correlationIDKey
	userIDKey
)

// Into stores log in ctx so downstream code can pick it up with From.
//
// Store the plain request logger here — never the result of From. From derives
// trace, correlation, and user fields from the context on every call, so
// storing its output would duplicate those keys on the next hop.
func Into(ctx context.Context, log *zap.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, log)
}

// From returns the logger stored in ctx, enriched with whatever request
// metadata the context carries: trace_id and span_id from the active
// OpenTelemetry span, plus correlation_id and user_id when set.
//
// It never returns nil. Without a stored logger it falls back to zap's global,
// which is a no-op logger until SetGlobal is called — so library code can log
// unconditionally without a nil check and without deciding where output goes.
func From(ctx context.Context) *zap.Logger {
	log, ok := ctx.Value(loggerKey).(*zap.Logger)
	if !ok || log == nil {
		log = zap.L()
	}

	fields := contextFields(ctx)
	if len(fields) == 0 {
		return log
	}

	return log.With(fields...)
}

// WithCorrelationID tags ctx with the ID that follows one logical operation
// across services, gRPC hops, and Kafka events.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

// CorrelationID returns the correlation ID in ctx, or "" if unset.
func CorrelationID(ctx context.Context) string {
	id, _ := ctx.Value(correlationIDKey).(string)

	return id
}

// WithUserID tags ctx with the authenticated user, so their requests can be
// traced through the logs of every service that handled them.
func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}

// UserID returns the user ID in ctx, or "" if unset.
func UserID(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey).(string)

	return id
}

func contextFields(ctx context.Context) []zap.Field {
	fields := make([]zap.Field, 0, 4)

	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		fields = append(fields,
			zap.String("trace_id", sc.TraceID().String()),
			zap.String("span_id", sc.SpanID().String()),
		)
	}
	if id := CorrelationID(ctx); id != "" {
		fields = append(fields, zap.String("correlation_id", id))
	}
	if id := UserID(ctx); id != "" {
		fields = append(fields, zap.String("user_id", id))
	}

	return fields
}
