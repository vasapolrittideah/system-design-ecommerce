// Package kafka is the driving adapter for order's own events: it decodes
// what pkg/events wrote and maps it to the use case it triggers, the same job
// adapter/in/grpc does for a request instead of a delivery.
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

// The names the domain's own events return from EventName() — the vocabulary
// this consumer dispatches on, repeated here rather than imported because this
// package maps proto to a use case and must not depend on the domain package
// to do it.
//
// OrderPlaced is deliberately absent: an order that exists has not been sold
// and has not been given back, so there is nothing for this service to do
// about it until one of these two says what it became.
const (
	eventTypeOrderPaid      = "OrderPaid"
	eventTypeOrderCancelled = "OrderCancelled"
)

// OrderEventsConsumer dispatches ecommerce.order.events.v1 back to this
// service's own use cases — the one topic order both publishes to and reads
// from, because the calls to inventory that finish a checkout have to survive
// the process dying between the transaction committing and the call that
// follows it.
type OrderEventsConsumer struct {
	saga in.CheckoutSaga
}

// NewOrderEventsConsumer builds the consumer over the use case it drives.
func NewOrderEventsConsumer(saga in.CheckoutSaga) *OrderEventsConsumer {
	return &OrderEventsConsumer{saga: saga}
}

// Handle is a kafkax.Handler: it decodes the envelope and dispatches on its
// event type.
//
// An event type this build does not recognise is skipped rather than
// retried — nothing about retrying would make it recognised — but a message
// that fails to decode is returned as an error, so kafkax's own retry-then-DLQ
// policy is what finally gives up on it rather than this adapter deciding to
// on the first attempt.
func (c *OrderEventsConsumer) Handle(ctx context.Context, msg kafkax.Message) error {
	envelope, err := events.Decode(msg.Value)
	if err != nil {
		return fmt.Errorf("order: decode envelope: %w", err)
	}

	ctx = events.Context(ctx, envelope)

	switch envelope.GetEventType() {
	case eventTypeOrderPaid:
		return c.handleOrderPaid(ctx, envelope)
	case eventTypeOrderCancelled:
		return c.handleOrderCancelled(ctx, envelope)
	default:
		logger.From(ctx).Debug("order: no handler for event type, skipping",
			zap.String("event_type", envelope.GetEventType()),
		)

		return nil
	}
}

func (c *OrderEventsConsumer) handleOrderPaid(ctx context.Context, envelope *eventsv1.EventEnvelope) error {
	var payload eventsv1.OrderPaid
	if err := envelope.GetPayload().UnmarshalTo(&payload); err != nil {
		return fmt.Errorf("order: unmarshal %s payload: %w", eventTypeOrderPaid, err)
	}

	if err := c.saga.CommitReservation(ctx, in.OrderPaidEvent{
		EventID:       envelope.GetEventId(),
		OrderID:       payload.GetOrderId(),
		ReservationID: payload.GetReservationId(),
	}); err != nil {
		return fmt.Errorf("order: commit reservation for order %s: %w", payload.GetOrderId(), err)
	}

	return nil
}

func (c *OrderEventsConsumer) handleOrderCancelled(ctx context.Context, envelope *eventsv1.EventEnvelope) error {
	var payload eventsv1.OrderCancelled
	if err := envelope.GetPayload().UnmarshalTo(&payload); err != nil {
		return fmt.Errorf("order: unmarshal %s payload: %w", eventTypeOrderCancelled, err)
	}

	if err := c.saga.ReleaseReservation(ctx, in.OrderCancelledEvent{
		EventID:       envelope.GetEventId(),
		OrderID:       payload.GetOrderId(),
		ReservationID: payload.GetReservationId(),
	}); err != nil {
		return fmt.Errorf("order: release reservation for order %s: %w", payload.GetOrderId(), err)
	}

	return nil
}
