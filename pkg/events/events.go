// Package events turns a domain event into the outbox row that will carry it.
//
// It is the one place an EventEnvelope is built, so that every topic in the
// system carries the same shape and a consumer needs one way to read a message
// whichever service raised it. A repository calls it while draining the events
// its aggregate accumulated, and writes the results in the transaction that
// persists the aggregate:
//
//	records := make([]outbox.Record, 0, len(pulled))
//	for _, e := range pulled {
//		record, err := events.Record(ctx, events.Fact{...})
//		...
//	}
//	return outbox.Write(ctx, db, records...)
//
// It sits between pkg/outbox and the generated events package on purpose:
// pkg/outbox may not know what a payload is, and a service's domain may not
// import either.
package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/events/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/outbox"
)

// HeaderCorrelationID and HeaderTraceparent are the message headers a consumer
// reads to attach its work to the operation that caused it. They repeat what
// the envelope already carries, deliberately: a dead-letter router or a
// debugging kafkacat reads a header without decoding the payload.
const (
	HeaderCorrelationID = "correlation_id"
	HeaderTraceparent   = "traceparent"
)

// ErrIncompleteFact reports a fact that cannot be recorded for want of a field.
// It is a programming error rather than a runtime condition.
var ErrIncompleteFact = errors.New("events: incomplete fact")

// Fact is one thing that happened, on its way to the outbox.
type Fact struct {
	// AggregateType and AggregateID identify what it happened to. The id is
	// also the message key, which is what keeps one aggregate's events in
	// order.
	AggregateType string
	AggregateID   string

	// EventType is the name consumers dispatch on, e.g. "UserRegistered". It is
	// API: renaming one is a breaking change no compiler reports.
	EventType string

	// Topic is where the event belongs, decided by the service that owns it.
	Topic string

	// Version is the aggregate's version at the moment the event was raised.
	Version int64

	// OccurredAt is when the fact became true, which is not when it is
	// published and not when it is consumed. Required, because a zero timestamp
	// on a topic is indistinguishable from the epoch.
	OccurredAt time.Time

	// Payload is the generated message from proto/ecommerce/events/v1.
	Payload proto.Message
}

// Record builds the outbox row for f, stamping it with a fresh event id and
// with the trace and correlation of ctx.
//
// The envelope is marshalled into the row's payload rather than assembled by
// the relay, so what a consumer receives is decided by the service that knows
// what happened — the relay only ever moves bytes.
func Record(ctx context.Context, f Fact) (outbox.Record, error) {
	if err := f.validate(); err != nil {
		return outbox.Record{}, err
	}

	payload, err := anypb.New(f.Payload)
	if err != nil {
		return outbox.Record{}, fmt.Errorf("events: wrap %s payload: %w", f.EventType, err)
	}

	headers := carry(ctx)

	envelope := &eventsv1.EventEnvelope{
		// Minted here, once, and never again: it is what a consumer claims in
		// processed_events, so a redelivery has to carry this same value while
		// a genuinely new event must not.
		EventId:       uuid.NewString(),
		EventType:     f.EventType,
		AggregateId:   f.AggregateID,
		Version:       f.Version,
		OccurredAt:    timestamppb.New(f.OccurredAt),
		CorrelationId: headers[HeaderCorrelationID],
		Traceparent:   headers[HeaderTraceparent],
		Payload:       payload,
	}

	encoded, err := proto.Marshal(envelope)
	if err != nil {
		return outbox.Record{}, fmt.Errorf("events: marshal %s envelope: %w", f.EventType, err)
	}

	return outbox.Record{
		AggregateType: f.AggregateType,
		AggregateID:   f.AggregateID,
		EventType:     f.EventType,
		Topic:         f.Topic,
		Payload:       encoded,
		Headers:       headers,
	}, nil
}

// Decode reads the envelope a producer wrote. It is the one place a message's
// bytes become an EventEnvelope, so every consumer in the system parses one
// the same way regardless of which topic it came from.
func Decode(payload []byte) (*eventsv1.EventEnvelope, error) {
	var envelope eventsv1.EventEnvelope
	if err := proto.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("events: unmarshal envelope: %w", err)
	}

	return &envelope, nil
}

// Context is carry's inverse: it puts what the envelope carried across the
// hop through Kafka back onto ctx, so a consumer's own calls attach to the
// trace that caused them and its logs carry the correlation id that ties them
// to everything else the same operation did.
//
// An envelope with neither set — published by a process that never started
// telemetry, or for a request that arrived without a correlation id — leaves
// ctx unchanged, the same as carry leaves the headers unchanged.
func Context(ctx context.Context, envelope *eventsv1.EventEnvelope) context.Context {
	if tp := envelope.GetTraceparent(); tp != "" {
		ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{HeaderTraceparent: tp})
	}

	if id := envelope.GetCorrelationId(); id != "" {
		ctx = logger.WithCorrelationID(ctx, id)
	}

	return ctx
}

func (f Fact) validate() error {
	var missing []string
	for _, field := range []struct {
		name  string
		empty bool
	}{
		{"AggregateType", f.AggregateType == ""},
		{"AggregateID", f.AggregateID == ""},
		{"EventType", f.EventType == ""},
		{"Topic", f.Topic == ""},
		{"OccurredAt", f.OccurredAt.IsZero()},
		{"Payload", f.Payload == nil},
	} {
		if field.empty {
			missing = append(missing, field.name)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrIncompleteFact, strings.Join(missing, ", "))
	}

	return nil
}

// carry collects what has to survive the hop through Kafka: the trace context,
// through the propagator observability.Start installed, and the correlation id,
// which travels beside it because it is this system's own and not W3C's.
//
// A process that never started telemetry injects nothing, and a request that
// arrived without a correlation id has none to pass on. Both leave the map
// shorter rather than failing: an event is a fact whether or not anyone is
// watching how it got here.
func carry(ctx context.Context) map[string]string {
	headers := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, headers)

	if id := logger.CorrelationID(ctx); id != "" {
		headers[HeaderCorrelationID] = id
	}

	return headers
}
