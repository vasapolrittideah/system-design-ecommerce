package rest

import (
	"time"

	commonv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/common/v1"
	orderv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/order/v1"
)

// moneyOf maps an amount that is always present, unlike toMoney: a price a
// product may not have is nullable, and a total an order always has is not.
func moneyOf(m *commonv1.Money) money {
	return money{
		AmountMinor:  m.GetAmountMinor(),
		CurrencyCode: m.GetCurrencyCode(),
	}
}

// checkoutRequest is what the buy button sends.
//
// It carries no user and no prices. Who is buying comes from the verified
// token, and what a thing costs is the catalog's answer — a client that could
// name its own price would be naming what it pays.
type checkoutRequest struct {
	// The client's own key for this attempt, echoed on every retry of it. It is
	// a field rather than a header because a BFF has no database and cannot
	// deduplicate: the key has to reach the service that owns the order, and it
	// travels in the request body all the way there.
	//
	// Required. A checkout without one is a checkout a network blip turns into
	// two orders.
	IdempotencyKey string `json:"idempotencyKey" validate:"required,min=8,max=128"`

	Lines []checkoutLine `json:"lines" validate:"required,min=1,max=100,dive"`
}

// checkoutLine is one SKU and how many of it.
type checkoutLine struct {
	SKU      string `json:"sku" validate:"required,max=64"`
	Quantity int32  `json:"quantity" validate:"required,gt=0,lte=10000"`
}

// orderPath is the order detail page's path parameter, checked through the same
// `validate` tags a body and a query string go through so the handler verifies
// nothing by hand either.
type orderPath struct {
	ID string `json:"id" validate:"required,uuid"`
}

// listOrdersQuery is the order history's query string.
type listOrdersQuery struct {
	// 0 leaves the page size to the order service, which is where the default
	// belongs.
	PageSize int32 `query:"pageSize" validate:"gte=0,lte=100"`

	// nextPageToken from the previous response, opaque to this tier as much as
	// to the client.
	PageToken string `query:"pageToken" validate:"omitempty,max=512"`
}

// What the order screens answer with.
type (
	orderResponse struct {
		Order order `json:"order"`
	}

	ordersResponse struct {
		Orders []order `json:"orders"`

		// Absent on the last page, and passed back as pageToken to ask for the
		// next one.
		NextPageToken string `json:"nextPageToken,omitempty"`
	}

	// order is one purchase as the confirmation and history screens need it.
	//
	// The reservation id is deliberately not here. It is how this system holds
	// stock while a payment happens, which is a fact about the machinery rather
	// than about the purchase, and a client that learned to read it would be a
	// client the saga cannot be changed underneath.
	order struct {
		ID     string `json:"id"`
		Status string `json:"status"`

		Lines []orderLine `json:"lines"`
		Total money       `json:"total"`

		CreatedAt time.Time `json:"createdAt"`
		UpdatedAt time.Time `json:"updatedAt"`
	}

	orderLine struct {
		SKU      string `json:"sku"`
		Quantity int32  `json:"quantity"`

		// What was agreed for one, frozen when the order was placed rather than
		// read from the catalog now.
		UnitPrice money `json:"unitPrice"`
	}
)

// toOrder shapes one order for a screen.
func toOrder(o *orderv1.Order) order {
	lines := make([]orderLine, 0, len(o.GetLines()))
	for _, line := range o.GetLines() {
		lines = append(lines, orderLine{
			SKU:       line.GetSku(),
			Quantity:  line.GetQuantity(),
			UnitPrice: moneyOf(line.GetUnitPrice()),
		})
	}

	return order{
		ID:        o.GetId(),
		Status:    toOrderStatus(o.GetStatus()),
		Lines:     lines,
		Total:     moneyOf(o.GetTotal()),
		CreatedAt: asTime(o.GetCreatedAt()),
		UpdatedAt: asTime(o.GetUpdatedAt()),
	}
}

// toOrderStatus renders the enum as the lowercase name a client branches on.
//
// A state this build does not know about becomes "unknown" rather than an
// error: an order the customer placed is still an order they should be able to
// see, and a screen that failed on a status a newer release wrote would be a
// worse answer than a label nobody recognises.
func toOrderStatus(status orderv1.OrderStatus) string {
	switch status {
	case orderv1.OrderStatus_ORDER_STATUS_PENDING_PAYMENT:
		return "pendingPayment"
	case orderv1.OrderStatus_ORDER_STATUS_PAID:
		return "paid"
	case orderv1.OrderStatus_ORDER_STATUS_CANCELLED:
		return "cancelled"
	default:
		return "unknown"
	}
}
