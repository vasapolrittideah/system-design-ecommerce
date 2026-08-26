package kafka

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	eventsv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/events/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/events"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/kafkax"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/in"
)

// The names the payment service's events travel under. Repeated here rather
// than imported from that service, which would be a compile-time dependency on
// another service's internals: what crosses the boundary is the contract in
// proto/ecommerce/events/v1 and the string on the envelope, and this is the
// string.
const (
	eventTypePaymentSucceeded = "PaymentSucceeded"
	eventTypePaymentFailed    = "PaymentFailed"
)

// PaymentEventsConsumer dispatches ecommerce.payment.events.v1 onto this
// service's own saga, which is where an order's state machine lives.
//
// The payment service publishes what became of the money and decides nothing
// about the order; deciding is this service's, because it owns the aggregate.
// That is the whole reason this consumer exists rather than payment writing an
// order status itself — and it is why only one of the two events it reads
// moves anything. Money arriving makes an order paid; a card being declined
// makes it nothing at all, because the customer may still try another.
type PaymentEventsConsumer struct {
	saga in.CheckoutSaga
}

// NewPaymentEventsConsumer builds the consumer over the use case it drives.
func NewPaymentEventsConsumer(saga in.CheckoutSaga) *PaymentEventsConsumer {
	return &PaymentEventsConsumer{saga: saga}
}

// Handle is a kafkax.Handler: it decodes the envelope and dispatches on its
// event type.
//
// An event type this build does not recognise is skipped rather than
// retried — nothing about retrying would make it recognised — but a message
// that fails to decode is returned as an error, so kafkax's own retry-then-DLQ
// policy is what finally gives up on it rather than this adapter deciding to
// on the first attempt.
func (c *PaymentEventsConsumer) Handle(ctx context.Context, msg kafkax.Message) error {
	envelope, err := events.Decode(msg.Value)
	if err != nil {
		return fmt.Errorf("order: decode envelope: %w", err)
	}

	ctx = events.Context(ctx, envelope)

	switch envelope.GetEventType() {
	case eventTypePaymentSucceeded:
		return c.handlePaymentSucceeded(ctx, envelope)
	case eventTypePaymentFailed:
		return c.handlePaymentFailed(ctx, envelope)

	default:
		logger.From(ctx).Debug("order: no handler for event type, skipping",
			zap.String("event_type", envelope.GetEventType()),
		)

		return nil
	}
}

func (c *PaymentEventsConsumer) handlePaymentSucceeded(
	ctx context.Context,
	envelope *eventsv1.EventEnvelope,
) error {
	var payload eventsv1.PaymentSucceeded
	if err := envelope.GetPayload().UnmarshalTo(&payload); err != nil {
		return fmt.Errorf("order: unmarshal %s payload: %w", eventTypePaymentSucceeded, err)
	}

	if err := c.saga.MarkPaid(ctx, in.PaymentSucceededEvent{
		EventID:   envelope.GetEventId(),
		OrderID:   payload.GetOrderId(),
		PaymentID: payload.GetPaymentId(),
	}); err != nil {
		return fmt.Errorf("order: mark order %s paid: %w", payload.GetOrderId(), err)
	}

	return nil
}

// handlePaymentFailed deliberately moves nothing.
//
// A declined card is not the end of an order. Declines are ordinary — a few
// percent of every card transaction, and most of them transient: funds that
// arrive an hour later, a 3-D Secure page that timed out, a bank rule that
// texts the customer to confirm. Every storefront worth copying lets the
// customer try another card against the same order, and payment's own schema
// is built for it: a failed attempt drops out of the index that allows one
// unresolved payment per order, precisely so the next one can begin.
//
// Cancelling here would close that door on the first decline and give the
// stock back while the customer was still reaching for another card. What ends
// an unpaid order is running out of time, which is the reservation's expires_at
// and the saga timeout that watches it — not this event.
//
// It is a named case rather than a fall-through to the default, so that the
// decision is visible where somebody would otherwise re-add the cancel. The
// payload is not decoded: nothing here reads it, and what a customer sees on
// the order screen comes from payment's own attempts, which the BFF reads
// directly.
func (c *PaymentEventsConsumer) handlePaymentFailed(
	ctx context.Context,
	envelope *eventsv1.EventEnvelope,
) error {
	logger.From(ctx).Info("order: a payment attempt failed, leaving the order open to another",
		zap.String("aggregate_id", envelope.GetAggregateId()),
	)

	return nil
}
