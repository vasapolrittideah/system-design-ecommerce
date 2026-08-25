// Package in declares the driving ports: what can be asked of this service,
// stated without reference to how the asking arrives. A gRPC handler maps its
// request into one of the commands below and calls the interface.
package in

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
)

// CheckoutLine is one SKU a caller wants to buy.
//
// It carries no price. What a thing costs is the catalog's answer, and a client
// that could name its own price would be naming what it pays.
type CheckoutLine struct {
	SKU      string
	Quantity int
}

// CheckoutCommand is a request to place an order.
type CheckoutCommand struct {
	// UserID is who is buying, taken from the verified identity on the call and
	// never from a field the client set.
	UserID string

	// IdempotencyKey is the client's own key for this attempt. The same key and
	// the same lines replay the first answer; the same key and different lines
	// are refused.
	IdempotencyKey string

	Lines []CheckoutLine
}

// GetOrderQuery reads one order.
type GetOrderQuery struct {
	OrderID string

	// UserID is who is asking. An order that belongs to somebody else is
	// answered as not found rather than as forbidden: telling a caller that an
	// order exists but is not theirs is telling them about another customer.
	UserID string
}

// ListOrdersQuery pages through one customer's own orders, newest first.
type ListOrdersQuery struct {
	UserID string

	// PageSize is clamped by the use case. Zero means the default.
	PageSize int

	// PageToken is the previous response's next token, opaque to the caller.
	PageToken string
}

// OrdersPage is one page of a customer's orders.
type OrdersPage struct {
	Orders []*domain.Order

	// NextPageToken is empty on the last page.
	NextPageToken string
}

// OrderUseCase is everything this service can be asked to do.
type OrderUseCase interface {
	// Checkout reserves the stock, persists the order in a pending state, and
	// returns. It does not wait for the purchase to complete: the rest of the
	// flow waits on a payment provider, and no caller should be held open for
	// that.
	Checkout(ctx context.Context, cmd CheckoutCommand) (*domain.Order, error)

	// GetOrder reads one of the caller's own orders.
	GetOrder(ctx context.Context, query GetOrderQuery) (*domain.Order, error)

	// ListOrders pages through the caller's own orders.
	ListOrders(ctx context.Context, query ListOrdersQuery) (OrdersPage, error)
}
