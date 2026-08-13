package logger_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

func observed(t *testing.T) (*zap.Logger, *observer.ObservedLogs) {
	t.Helper()

	core, logs := observer.New(zap.DebugLevel)

	return zap.New(core), logs
}

// fields flattens the fields of the single line recorded in logs.
func fields(t *testing.T, logs *observer.ObservedLogs) map[string]any {
	t.Helper()

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("got %d lines, want 1", len(entries))
	}

	return entries[0].ContextMap()
}

func sampledContext(t *testing.T) (context.Context, string, string) {
	t.Helper()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("parse trace id: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("parse span id: %v", err)
	}

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})

	return trace.ContextWithSpanContext(context.Background(), sc), traceID.String(), spanID.String()
}

func TestFromReturnsStoredLogger(t *testing.T) {
	log, logs := observed(t)

	logger.From(logger.Into(context.Background(), log)).Info("hello")

	if got := fields(t, logs); len(got) != 0 {
		t.Errorf("fields = %v, want none for a bare context", got)
	}
}

func TestFromFallsBackToGlobal(t *testing.T) {
	log, logs := observed(t)
	restore := logger.SetGlobal(log)
	defer restore()

	logger.From(context.Background()).Info("hello")

	if logs.Len() != 1 {
		t.Errorf("got %d lines, want the global logger to receive it", logs.Len())
	}
}

func TestFromNeverReturnsNil(t *testing.T) {
	// A context holding a nil logger must not panic the caller.
	//nolint:staticcheck // deliberately storing a nil logger
	ctx := logger.Into(context.Background(), nil)

	if logger.From(ctx) == nil {
		t.Fatal("From() = nil, want a usable logger")
	}
}

func TestFromAttachesTraceFields(t *testing.T) {
	log, logs := observed(t)
	ctx, traceID, spanID := sampledContext(t)

	logger.From(logger.Into(ctx, log)).Info("hello")

	got := fields(t, logs)
	if got["trace_id"] != traceID {
		t.Errorf("trace_id = %v, want %q", got["trace_id"], traceID)
	}
	if got["span_id"] != spanID {
		t.Errorf("span_id = %v, want %q", got["span_id"], spanID)
	}
}

func TestFromAttachesRequestMetadata(t *testing.T) {
	log, logs := observed(t)

	ctx := logger.WithUserID(
		logger.WithCorrelationID(context.Background(), "corr-1"),
		"user-1",
	)
	logger.From(logger.Into(ctx, log)).Info("hello")

	got := fields(t, logs)
	if got["correlation_id"] != "corr-1" {
		t.Errorf("correlation_id = %v, want %q", got["correlation_id"], "corr-1")
	}
	if got["user_id"] != "user-1" {
		t.Errorf("user_id = %v, want %q", got["user_id"], "user-1")
	}
}

func TestFromOmitsUnsetMetadata(t *testing.T) {
	log, logs := observed(t)

	ctx := logger.WithCorrelationID(context.Background(), "")
	logger.From(logger.Into(ctx, log)).Info("hello")

	if _, ok := fields(t, logs)["correlation_id"]; ok {
		t.Error("correlation_id present, want empty values omitted")
	}
}

func TestFromDoesNotDuplicateFieldsAcrossHops(t *testing.T) {
	log, logs := observed(t)
	ctx, _, _ := sampledContext(t)
	ctx = logger.Into(logger.WithCorrelationID(ctx, "corr-1"), log)

	// Documented usage: the stored logger stays plain, so deriving twice cannot
	// double up the context fields.
	logger.From(ctx)
	logger.From(ctx).Info("hello")

	entry := logs.All()[0]
	seen := make(map[string]int, len(entry.Context))
	for _, field := range entry.Context {
		seen[field.Key]++
	}
	for key, count := range seen {
		if count > 1 {
			t.Errorf("field %q appears %d times, want once", key, count)
		}
	}
}

func TestCorrelationIDAndUserIDRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want string
		got  func(context.Context) string
	}{
		{
			name: "correlation id set",
			ctx:  logger.WithCorrelationID(context.Background(), "corr-1"),
			want: "corr-1",
			got:  logger.CorrelationID,
		},
		{
			name: "correlation id unset",
			ctx:  context.Background(),
			want: "",
			got:  logger.CorrelationID,
		},
		{
			name: "user id set",
			ctx:  logger.WithUserID(context.Background(), "user-1"),
			want: "user-1",
			got:  logger.UserID,
		},
		{
			name: "user id unset",
			ctx:  context.Background(),
			want: "",
			got:  logger.UserID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.got(tt.ctx); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKeysDoNotCollideAcrossPackages(t *testing.T) {
	// An unkeyed string would let any package overwrite these values.
	type otherKey string

	ctx := context.WithValue(context.Background(), otherKey("user_id"), "attacker")

	if got := logger.UserID(ctx); got != "" {
		t.Errorf("UserID() = %q, want %q — context keys are not package-private", got, "")
	}
}
