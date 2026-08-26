package kafka_test

import (
	"context"
	"errors"
	"testing"

	eventsv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/events/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/kafkax"
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/adapter/in/kafka"
)

func TestHandleDispatchesPaymentSucceededToTheSaga(t *testing.T) {
	saga := &fakeSaga{}
	c := adapter.NewPaymentEventsConsumer(saga)

	msg := envelopeMessage(t, "PaymentSucceeded", &eventsv1.PaymentSucceeded{
		PaymentId: "payment-1",
		OrderId:   "order-1",
	})

	if err := c.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}

	if saga.marks != 1 {
		t.Fatalf("MarkPaid was called %d time(s), want 1", saga.marks)
	}
	if saga.paidByPayment.EventID != "9c1a2b3d-4e5f-4061-8a2b-3c4d5e6f7a8b" {
		t.Errorf("event id = %q, want the envelope's", saga.paidByPayment.EventID)
	}
	if saga.paidByPayment.OrderID != "order-1" || saga.paidByPayment.PaymentID != "payment-1" {
		t.Errorf("event = %+v, want order-1 / payment-1", saga.paidByPayment)
	}
	// The order's own state machine is what this drives. Touching inventory
	// here would be the commit made straight after a transaction, which is the
	// write nothing would retry if the process died between the two.
	if saga.commits != 0 || saga.releases != 0 {
		t.Errorf("inventory was driven (%d commits, %d releases), want none", saga.commits, saga.releases)
	}
}

// A declined card leaves the order exactly where it was, so that the customer
// can try another one — payment's own schema is built for that, since a failed
// attempt drops out of the index allowing one unresolved payment per order.
// What ends an unpaid order is running out of time, not one attempt failing.
func TestHandleLeavesTheOrderAloneOnPaymentFailed(t *testing.T) {
	saga := &fakeSaga{}
	c := adapter.NewPaymentEventsConsumer(saga)

	msg := envelopeMessage(t, "PaymentFailed", &eventsv1.PaymentFailed{
		PaymentId: "payment-1",
		OrderId:   "order-1",
		Reason:    "card_declined",
	})

	if err := c.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}

	// Accepted rather than retried: the delivery was understood, and there was
	// nothing to do about it. Returning an error here would send every decline
	// to the DLQ.
	if saga.marks != 0 || saga.commits != 0 || saga.releases != 0 {
		t.Errorf("the saga was driven (%d marks, %d commits, %d releases), want none",
			saga.marks, saga.commits, saga.releases)
	}
}

// This consumer reads the payment topic. An order event reaching it — a
// misconfigured KAFKA_PAYMENT_CONSUMER_TOPIC would do it — is skipped rather
// than acted on.
func TestThePaymentConsumerIgnoresOrderEvents(t *testing.T) {
	saga := &fakeSaga{}
	c := adapter.NewPaymentEventsConsumer(saga)

	msg := envelopeMessage(t, "OrderPaid", &eventsv1.OrderPaid{OrderId: "order-1"})

	if err := c.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle() error = %v, want nil", err)
	}
	if saga.marks != 0 {
		t.Errorf("saga was driven %d time(s), want none", saga.marks)
	}
}

func TestThePaymentConsumerPropagatesTheSagaFailure(t *testing.T) {
	wantErr := errors.New("order was cancelled")
	saga := &fakeSaga{returns: wantErr}
	c := adapter.NewPaymentEventsConsumer(saga)

	msg := envelopeMessage(t, "PaymentSucceeded", &eventsv1.PaymentSucceeded{OrderId: "order-1"})

	err := c.Handle(context.Background(), msg)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Handle() error = %v, want it to wrap %v", err, wantErr)
	}
}

func TestThePaymentConsumerReturnsAnErrorForBytesThatAreNotAnEnvelope(t *testing.T) {
	saga := &fakeSaga{}
	c := adapter.NewPaymentEventsConsumer(saga)

	err := c.Handle(context.Background(), kafkax.Message{Value: []byte{0xff, 0xff, 0xff}})
	if err == nil {
		t.Fatal("Handle() error = nil, want a rejection")
	}
	if saga.marks != 0 {
		t.Errorf("saga was driven %d time(s), want none", saga.marks)
	}
}

func TestThePaymentConsumerReturnsAnErrorForAMalformedPayload(t *testing.T) {
	saga := &fakeSaga{}
	c := adapter.NewPaymentEventsConsumer(saga)

	// A well-formed envelope whose event type says PaymentSucceeded but whose
	// payload is some other message.
	msg := envelopeMessage(t, "PaymentSucceeded", &eventsv1.OrderLine{Sku: "not-a-payment"})

	err := c.Handle(context.Background(), msg)
	if err == nil {
		t.Fatal("Handle() error = nil, want a rejection")
	}
	if saga.marks != 0 {
		t.Errorf("MarkPaid was called %d time(s), want 0", saga.marks)
	}
}
