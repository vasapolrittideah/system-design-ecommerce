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
	paid      in.OrderPaidEvent
	cancelled in.OrderCancelledEvent
	commits   int
	releases  int
	returns   error
}

func (f *fakeSaga) CommitReservation(_ context.Context, event in.OrderPaidEvent) error {
	f.paid = event
	f.commits++

	return f.returns
}

func (f *fakeSaga) ReleaseReservation(_ context.Context, event in.OrderCancelledEvent) error {
	f.cancelled = event
	f.releases++

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

func TestHandleDispatchesOrderPaidToTheSaga(t *testing.T) {
	saga := &fakeSaga{}
	c := adapter.NewOrderEventsConsumer(saga)

	msg := envelopeMessage(t, "OrderPaid", &eventsv1.OrderPaid{
		OrderId:       "order-1",
		ReservationId: "reservation-1",
	})

	if err := c.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}

	if saga.commits != 1 {
		t.Fatalf("CommitReservation was called %d time(s), want 1", saga.commits)
	}
	if saga.paid.EventID != "9c1a2b3d-4e5f-4061-8a2b-3c4d5e6f7a8b" {
		t.Errorf("event id = %q, want the envelope's", saga.paid.EventID)
	}
	if saga.paid.OrderID != "order-1" || saga.paid.ReservationID != "reservation-1" {
		t.Errorf("event = %+v, want order-1 / reservation-1", saga.paid)
	}
}

func TestHandleDispatchesOrderCancelledToTheSaga(t *testing.T) {
	saga := &fakeSaga{}
	c := adapter.NewOrderEventsConsumer(saga)

	msg := envelopeMessage(t, "OrderCancelled", &eventsv1.OrderCancelled{
		OrderId:       "order-1",
		ReservationId: "reservation-1",
	})

	if err := c.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}

	if saga.releases != 1 {
		t.Fatalf("ReleaseReservation was called %d time(s), want 1", saga.releases)
	}
	if saga.commits != 0 {
		t.Errorf("CommitReservation was called %d time(s), want 0", saga.commits)
	}
	if saga.cancelled.OrderID != "order-1" || saga.cancelled.ReservationID != "reservation-1" {
		t.Errorf("event = %+v, want order-1 / reservation-1", saga.cancelled)
	}
}

// An order that merely exists is not a reason to touch inventory: the hold was
// taken before it was persisted, and what becomes of it is said by OrderPaid or
// OrderCancelled.
func TestHandleIgnoresOrderPlaced(t *testing.T) {
	saga := &fakeSaga{}
	c := adapter.NewOrderEventsConsumer(saga)

	msg := envelopeMessage(t, "OrderPlaced", &eventsv1.OrderPlaced{
		OrderId:       "order-1",
		ReservationId: "reservation-1",
	})

	if err := c.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if saga.commits != 0 || saga.releases != 0 {
		t.Errorf("saga was called (%d commits, %d releases), want none", saga.commits, saga.releases)
	}
}

func TestHandlePropagatesTheSagaFailure(t *testing.T) {
	wantErr := errors.New("commit failed")
	saga := &fakeSaga{returns: wantErr}
	c := adapter.NewOrderEventsConsumer(saga)

	msg := envelopeMessage(t, "OrderPaid", &eventsv1.OrderPaid{OrderId: "order-1", ReservationId: "reservation-1"})

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

	msg := envelopeMessage(t, "OrderRefunded", nil)

	if err := c.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if saga.commits != 0 || saga.releases != 0 {
		t.Errorf("saga was called (%d commits, %d releases), want none", saga.commits, saga.releases)
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
	if saga.commits != 0 {
		t.Errorf("CommitReservation was called %d time(s), want 0", saga.commits)
	}
}

func TestHandleReturnsAnErrorForAMalformedOrderPaidPayload(t *testing.T) {
	saga := &fakeSaga{}
	c := adapter.NewOrderEventsConsumer(saga)

	// A well-formed envelope whose event type says OrderPaid but whose payload
	// is some other message.
	msg := envelopeMessage(t, "OrderPaid", &eventsv1.OrderLine{Sku: "not-an-order-paid"})

	err := c.Handle(context.Background(), msg)
	if err == nil {
		t.Fatal("Handle() error = nil, want a rejection")
	}
	if saga.commits != 0 {
		t.Errorf("CommitReservation was called %d time(s), want 0", saga.commits)
	}
}
