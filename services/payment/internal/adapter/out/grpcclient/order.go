// Package grpcclient is the driven adapter for the services this one calls: it
// maps between the domain's vocabulary and the generated clients, and lets the
// failures the far end classified travel back unflattened.
//
// It is the only package here that knows another service exists.
package grpcclient

import (
	"context"

	orderv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/order/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/port/out"
)

// OrderGateway calls the order service.
type OrderGateway struct {
	client orderv1.OrderServiceClient
}

var _ out.OrderGateway = (*OrderGateway)(nil)

// NewOrderGateway builds the gateway over a generated client.
func NewOrderGateway(client orderv1.OrderServiceClient) *OrderGateway {
	return &OrderGateway{client: client}
}

// GetOrder reads one order.
//
// The error is returned as it arrived. An order that is not the caller's comes
// back as NotFound from the order service itself, and rewrapping it would cost
// the caller the distinction between an order that does not exist and one this
// service could not reach — errorx.ToGRPC keeps a status that is already a
// status for exactly this reason.
func (g *OrderGateway) GetOrder(ctx context.Context, orderID domain.OrderID) (out.PayableOrder, error) {
	res, err := g.client.GetOrder(ctx, &orderv1.GetOrderRequest{Id: orderID.String()})
	if err != nil {
		return out.PayableOrder{}, err
	}

	order := res.GetOrder()

	total, err := domain.NewCurrencyCode(order.GetTotal().GetCurrencyCode())
	if err != nil {
		return out.PayableOrder{}, err
	}

	amount, err := domain.NewMoney(order.GetTotal().GetAmountMinor(), total)
	if err != nil {
		return out.PayableOrder{}, err
	}

	return out.PayableOrder{
		ID:      domain.OrderID(order.GetId()),
		Total:   amount,
		Payable: payable(order.GetStatus()),
	}, nil
}

// payable answers whether an order is still waiting to be paid for.
//
// The translation stops here rather than travelling inward. This is the one
// place in the service that may know the order service's states, and a copy of
// its state machine further in would be one that stops agreeing with the
// original the first time a state is added — quietly, since nothing about a
// new enum value breaks a build.
//
// An unrecognised state is not payable. A build that does not know what an
// order is doing must not conclude that charging for it is fine.
func payable(status orderv1.OrderStatus) bool {
	return status == orderv1.OrderStatus_ORDER_STATUS_PENDING_PAYMENT
}
