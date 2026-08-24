package events_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/events/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/events"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

var occurredAt = time.Date(2026, 8, 24, 10, 30, 0, 0, time.UTC)

func fact() events.Fact {
	return events.Fact{
		AggregateType: "user",
		AggregateID:   "01234567-89ab-cdef-0123-456789abcdef",
		EventType:     "UserRegistered",
		Topic:         "ecommerce.identity.events.v1",
		Version:       1,
		OccurredAt:    occurredAt,
		Payload: &eventsv1.UserRegistered{
			UserId:       "01234567-89ab-cdef-0123-456789abcdef",
			Email:        "someone@example.com",
			Roles:        []string{"customer"},
			RegisteredAt: timestamppb.New(occurredAt),
		},
	}
}

func envelopeOf(t *testing.T, payload []byte) *eventsv1.EventEnvelope {
	t.Helper()

	var envelope eventsv1.EventEnvelope
	if err := proto.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	return &envelope
}

func TestRecordCarriesTheFactIntoTheEnvelope(t *testing.T) {
	want := fact()

	record, err := events.Record(context.Background(), want)
	if err != nil {
		t.Fatalf("Record() error = %v, want nil", err)
	}

	// The row's own columns are what the relay routes and keys on, without
	// opening the payload.
	if record.Topic != want.Topic {
		t.Errorf("topic = %q, want %q", record.Topic, want.Topic)
	}
	if record.AggregateID != want.AggregateID {
		t.Errorf("aggregate id = %q, want %q", record.AggregateID, want.AggregateID)
	}
	if record.EventType != want.EventType {
		t.Errorf("event type = %q, want %q", record.EventType, want.EventType)
	}

	envelope := envelopeOf(t, record.Payload)
	if envelope.GetEventType() != want.EventType {
		t.Errorf("envelope event_type = %q, want %q", envelope.GetEventType(), want.EventType)
	}
	if envelope.GetAggregateId() != want.AggregateID {
		t.Errorf("envelope aggregate_id = %q, want %q", envelope.GetAggregateId(), want.AggregateID)
	}
	if envelope.GetVersion() != want.Version {
		t.Errorf("envelope version = %d, want %d", envelope.GetVersion(), want.Version)
	}
	if got := envelope.GetOccurredAt().AsTime(); !got.Equal(occurredAt) {
		t.Errorf("envelope occurred_at = %v, want %v", got, occurredAt)
	}

	var payload eventsv1.UserRegistered
	if err := envelope.GetPayload().UnmarshalTo(&payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.GetEmail() != "someone@example.com" {
		t.Errorf("payload email = %q, want %q", payload.GetEmail(), "someone@example.com")
	}
}

func TestRecordMintsAnEventIDPerCall(t *testing.T) {
	ctx := context.Background()

	first, err := events.Record(ctx, fact())
	if err != nil {
		t.Fatalf("Record() error = %v, want nil", err)
	}
	second, err := events.Record(ctx, fact())
	if err != nil {
		t.Fatalf("Record() error = %v, want nil", err)
	}

	firstID := envelopeOf(t, first.Payload).GetEventId()
	secondID := envelopeOf(t, second.Payload).GetEventId()

	if firstID == "" {
		t.Fatal("event_id is empty, want a fresh id")
	}
	// Two raisings of the same fact are two events. Sharing an id would make a
	// consumer claim the first and discard the second as a redelivery.
	if firstID == secondID {
		t.Errorf("both events carry event_id %q, want one each", firstID)
	}
}

func TestRecordCarriesCorrelationAndTrace(t *testing.T) {
	// The propagator observability.Start would install. Without one, Inject
	// writes nothing and the trace stops here.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	tracer := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample())).Tracer("test")
	ctx, span := tracer.Start(logger.WithCorrelationID(context.Background(), "corr-1"), "register")
	defer span.End()

	record, err := events.Record(ctx, fact())
	if err != nil {
		t.Fatalf("Record() error = %v, want nil", err)
	}

	traceID := span.SpanContext().TraceID().String()

	// Both places, and both are read: the header by anything routing a message
	// without decoding it, the envelope field by the consumer that does.
	if got := record.Headers[events.HeaderCorrelationID]; got != "corr-1" {
		t.Errorf("header correlation_id = %q, want %q", got, "corr-1")
	}
	traceparent := record.Headers[events.HeaderTraceparent]
	if traceparent == "" {
		t.Fatal("header traceparent is empty, want the current trace")
	}
	if !strings.Contains(traceparent, traceID) {
		t.Errorf("header traceparent = %q, want it to carry trace id %s", traceparent, traceID)
	}

	envelope := envelopeOf(t, record.Payload)
	if envelope.GetCorrelationId() != "corr-1" {
		t.Errorf("envelope correlation_id = %q, want %q", envelope.GetCorrelationId(), "corr-1")
	}
	if envelope.GetTraceparent() != traceparent {
		t.Errorf("envelope traceparent = %q, want %q", envelope.GetTraceparent(), traceparent)
	}
}

// An event is a fact whether or not anything was watching how it got raised.
func TestRecordWithoutTraceOrCorrelationStillRecords(t *testing.T) {
	record, err := events.Record(context.Background(), fact())
	if err != nil {
		t.Fatalf("Record() error = %v, want nil", err)
	}

	if got := record.Headers[events.HeaderCorrelationID]; got != "" {
		t.Errorf("header correlation_id = %q, want it absent", got)
	}
	if len(record.Payload) == 0 {
		t.Error("payload is empty, want the envelope")
	}
}

func TestRecordRejectsIncompleteFacts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*events.Fact)
	}{
		{"no aggregate type", func(f *events.Fact) { f.AggregateType = "" }},
		{"no aggregate id", func(f *events.Fact) { f.AggregateID = "" }},
		{"no event type", func(f *events.Fact) { f.EventType = "" }},
		{"no topic", func(f *events.Fact) { f.Topic = "" }},
		{"no payload", func(f *events.Fact) { f.Payload = nil }},
		// A zero timestamp on a topic is indistinguishable from the epoch.
		{"no occurred at", func(f *events.Fact) { f.OccurredAt = time.Time{} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := fact()
			tt.mutate(&f)

			if _, err := events.Record(context.Background(), f); !errors.Is(err, events.ErrIncompleteFact) {
				t.Fatalf("Record() error = %v, want ErrIncompleteFact", err)
			}
		})
	}
}
