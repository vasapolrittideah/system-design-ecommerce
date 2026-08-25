package grpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/common/v1"
	orderv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/order/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/in"
)

// Checkout places an order for the caller.
func (h *OrderHandler) Checkout(
	ctx context.Context,
	req *orderv1.CheckoutRequest,
) (*orderv1.CheckoutResponse, error) {
	userID, err := caller(ctx)
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	lines := make([]in.CheckoutLine, 0, len(req.GetLines()))
	for _, line := range req.GetLines() {
		lines = append(lines, in.CheckoutLine{SKU: line.GetSku(), Quantity: int(line.GetQuantity())})
	}

	order, err := h.orders.Checkout(ctx, in.CheckoutCommand{
		UserID:         userID,
		IdempotencyKey: req.GetIdempotencyKey(),
		Lines:          lines,
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &orderv1.CheckoutResponse{Order: toProto(order)}, nil
}

// GetOrder reads one of the caller's own orders.
func (h *OrderHandler) GetOrder(
	ctx context.Context,
	req *orderv1.GetOrderRequest,
) (*orderv1.GetOrderResponse, error) {
	userID, err := caller(ctx)
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	order, err := h.orders.GetOrder(ctx, in.GetOrderQuery{OrderID: req.GetId(), UserID: userID})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &orderv1.GetOrderResponse{Order: toProto(order)}, nil
}

// ListOrders pages through the caller's own orders.
func (h *OrderHandler) ListOrders(
	ctx context.Context,
	req *orderv1.ListOrdersRequest,
) (*orderv1.ListOrdersResponse, error) {
	userID, err := caller(ctx)
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	page, err := h.orders.ListOrders(ctx, in.ListOrdersQuery{
		UserID:    userID,
		PageSize:  int(req.GetPageSize()),
		PageToken: req.GetPageToken(),
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	orders := make([]*orderv1.Order, 0, len(page.Orders))
	for _, order := range page.Orders {
		orders = append(orders, toProto(order))
	}

	return &orderv1.ListOrdersResponse{Orders: orders, NextPageToken: page.NextPageToken}, nil
}

// caller is who the request is for.
//
// It comes from the identity the interceptor put on the context — forwarded
// from the edge, where a BFF verified the token — and never from the request.
// Every method here is about somebody's own orders, so a call arriving with no
// identity has nothing to be about: that is Unauthenticated rather than a
// listing of everybody's orders.
//
// This service does not verify tokens and does not answer 401 for an expired
// one. Identity was settled a hop earlier; what is missing here is a caller at
// all, which is what a relay or a timeout worker looks like, and neither of
// those has any business asking these questions.
func caller(ctx context.Context) (string, error) {
	identity, ok := grpcx.IdentityFrom(ctx)
	if !ok || identity.UserID == "" {
		return "", errorx.New(errorx.KindUnauthenticated, "this call is about the caller's own orders").
			WithReason("IDENTITY_REQUIRED")
	}

	return identity.UserID, nil
}

func toProto(order *domain.Order) *orderv1.Order {
	lines := make([]*orderv1.OrderLine, 0, len(order.Lines()))
	for _, line := range order.Lines() {
		lines = append(lines, &orderv1.OrderLine{
			Sku:      line.SKU().String(),
			Quantity: narrow(line.Quantity()),
			UnitPrice: &commonv1.Money{
				AmountMinor:  line.UnitPrice().AmountMinor(),
				CurrencyCode: line.UnitPrice().Currency().String(),
			},
		})
	}

	return &orderv1.Order{
		Id:     order.ID().String(),
		UserId: order.UserID().String(),
		Status: toProtoStatus(order.Status()),
		Lines:  lines,
		Total: &commonv1.Money{
			AmountMinor:  order.Total().AmountMinor(),
			CurrencyCode: order.Total().Currency().String(),
		},
		ReservationId: order.ReservationID().String(),
		CreatedAt:     timestamppb.New(order.CreatedAt()),
		UpdatedAt:     timestamppb.New(order.UpdatedAt()),
	}
}

// toProtoStatus maps the domain's vocabulary onto the contract's.
//
// A state the contract does not name becomes UNSPECIFIED rather than a panic or
// a guess: an order in a state this build does not know about is still an order
// the customer can be shown, and the alternative is a screen that fails on data
// a newer version wrote.
func toProtoStatus(status domain.Status) orderv1.OrderStatus {
	switch status {
	case domain.StatusPendingPayment:
		return orderv1.OrderStatus_ORDER_STATUS_PENDING_PAYMENT
	case domain.StatusPaid:
		return orderv1.OrderStatus_ORDER_STATUS_PAID
	case domain.StatusCancelled:
		return orderv1.OrderStatus_ORDER_STATUS_CANCELLED
	default:
		return orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}

// narrow converts a count the domain keeps as an int to the int32 the contract
// declares, clamping rather than wrapping.
func narrow(value int) int32 {
	const maxInt32 = 1<<31 - 1

	switch {
	case value < 0:
		return 0
	case value > maxInt32:
		return maxInt32
	default:
		return int32(value)
	}
}
