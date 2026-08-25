package kafka_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/events/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/kafkax"
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/adapter/in/kafka"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/in"
)

// fakeSaga is a hand-rolled test double rather than a mockery mock: nothing in
// .mockery.yml generates one for a driving port, which only ever has one real
// caller — the adapter that drives it — and here that caller is this test.
type fakeSaga struct {
	got     in.OrderPlacedEvent
	calls   int
	returns error
}

func (f *fakeSaga) CommitReservation(_ context.Context, event in.OrderPlacedEvent) error {
	f.got = event
	f.calls++

	return f.returns
}

func envelopeMessage(t *testing.T, eventType string, payload proto.Message) kafkax.Message {
	t.Helper()

	var any *anypb.Any
	if payload != nil {
		var err error
		any, err = anypb.New(payload)
		if err != nil {
			t.Fatalf("anypb.New() error = %v, want nil", err)
		}
	}

	encoded, err := proto.Marshal(&eventsv1.EventEnvelope{
		EventId:     "9c1a2b3d-4e5f-4061-8a2b-3c4d5e6f7a8b",
		EventType:   eventType,
		AggregateId: "order-1",
		OccurredAt:  timestamppb.Now(),
		Payload:     any,
	})
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v, want nil", err)
	}

	return kafkax.Message{Topic: "ecommerce.order.events.v1", Key: []byte("order-1"), Value: encoded}
}

func TestHandleDispatchesOrderPlacedToTheSaga(t *testing.T) {
	saga := &fakeSaga{}
	c := adapter.NewOrderEventsConsumer(saga)

	msg := envelopeMessage(t, "OrderPlaced", &eventsv1.OrderPlaced{
		OrderId:       "order-1",
		ReservationId: "reservation-1",
	})

	if err := c.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}

	if saga.calls != 1 {
		t.Fatalf("CommitReservation was called %d time(s), want 1", saga.calls)
	}
	if saga.got.EventID != "9c1a2b3d-4e5f-4061-8a2b-3c4d5e6f7a8b" {
		t.Errorf("event id = %q, want the envelope's", saga.got.EventID)
	}
	if saga.got.OrderID != "order-1" || saga.got.ReservationID != "reservation-1" {
		t.Errorf("event = %+v, want order-1 / reservation-1", saga.got)
	}
}

func TestHandlePropagatesTheSagaFailure(t *testing.T) {
	wantErr := errors.New("commit failed")
	saga := &fakeSaga{returns: wantErr}
	c := adapter.NewOrderEventsConsumer(saga)

	msg := envelopeMessage(t, "OrderPlaced", &eventsv1.OrderPlaced{OrderId: "order-1", ReservationId: "reservation-1"})

	err := c.Handle(context.Background(), msg)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Handle() error = %v, want it to wrap %v", err, wantErr)
	}
}

// An event type this build does not know about is skipped rather than
// retried — forward compatibility, not a failure.
func TestHandleSkipsAnUnknownEventType(t *testing.T) {
	saga := &fakeSaga{}
	c := adapter.NewOrderEventsConsumer(saga)

	msg := envelopeMessage(t, "OrderCancelled", nil)

	if err := c.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if saga.calls != 0 {
		t.Errorf("CommitReservation was called %d time(s), want 0", saga.calls)
	}
}

// A message that does not even decode as an envelope is returned as an
// error, so kafkax's own retry-then-DLQ policy is what gives up on it.
func TestHandleReturnsAnErrorForBytesThatAreNotAnEnvelope(t *testing.T) {
	saga := &fakeSaga{}
	c := adapter.NewOrderEventsConsumer(saga)

	err := c.Handle(context.Background(), kafkax.Message{Value: []byte{0xff, 0xff, 0xff}})
	if err == nil {
		t.Fatal("Handle() error = nil, want a rejection")
	}
	if saga.calls != 0 {
		t.Errorf("CommitReservation was called %d time(s), want 0", saga.calls)
	}
}

func TestHandleReturnsAnErrorForAMalformedOrderPlacedPayload(t *testing.T) {
	saga := &fakeSaga{}
	c := adapter.NewOrderEventsConsumer(saga)

	// A well-formed envelope whose payload's type_url names OrderPlaced but
	// whose bytes are not one.
	msg := envelopeMessage(t, "OrderPlaced", &eventsv1.OrderLine{Sku: "not-an-order-placed"})

	err := c.Handle(context.Background(), msg)
	if err == nil {
		t.Fatal("Handle() error = nil, want a rejection")
	}
	if saga.calls != 0 {
		t.Errorf("CommitReservation was called %d time(s), want 0", saga.calls)
	}
}
