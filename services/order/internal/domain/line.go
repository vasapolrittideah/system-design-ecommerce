package domain

import "math"

// maxQuantity is the most of one SKU a single line may carry, matching the
// bound the proto declares. It is repeated here because a domain that trusted
// the transport to have checked would be a domain with no rule at all.
const maxQuantity = 10000

// OrderLine is one SKU, how many of it, and what was agreed for each.
//
// Its fields are unexported because the three are one fact: a quantity without
// a price is not a line of an order, and a price that could be changed after
// the fact would rewrite what the customer agreed to.
type OrderLine struct {
	sku       SKU
	quantity  int
	unitPrice Money
}

// NewOrderLine builds a line.
//
// The price is the catalog's answer at the moment of checkout, copied in by the
// use case. Nothing here can look it up, and that is the point: an order is a
// record of an agreement, and an agreement that re-read the price would not be
// one.
func NewOrderLine(sku SKU, quantity int, unitPrice Money) (OrderLine, error) {
	if sku == "" {
		return OrderLine{}, ValidationError{Field: "sku", Message: "is required"}
	}

	if quantity <= 0 {
		return OrderLine{}, ValidationError{Field: "quantity", Message: "must be greater than zero"}
	}
	if quantity > maxQuantity {
		return OrderLine{}, ValidationError{Field: "quantity", Message: "is above the per-line maximum"}
	}

	if unitPrice.IsZero() {
		return OrderLine{}, ValidationError{Field: "unit_price", Message: "is required"}
	}

	return OrderLine{sku: sku, quantity: quantity, unitPrice: unitPrice}, nil
}

// ReconstituteOrderLine rebuilds a line from storage, validating nothing, so
// that a rule tightened afterwards cannot make an existing order unreadable.
func ReconstituteOrderLine(sku SKU, quantity int, unitPrice Money) OrderLine {
	return OrderLine{sku: sku, quantity: quantity, unitPrice: unitPrice}
}

// SKU returns what the line is for.
func (l OrderLine) SKU() SKU { return l.sku }

// Quantity returns how many were bought.
func (l OrderLine) Quantity() int { return l.quantity }

// UnitPrice returns the price agreed for one, frozen at checkout.
func (l OrderLine) UnitPrice() Money { return l.unitPrice }

// Subtotal is the price of the line.
//
// It is computed rather than stored, because a stored subtotal is a third
// number that can disagree with the two it comes from. The overflow check is
// not theatre: the bounds above allow a line big enough to wrap int64 if a
// price is absurd, and an amount that wrapped is a charge for the wrong money
// rather than an error anyone would see.
func (l OrderLine) Subtotal() (Money, error) {
	if l.unitPrice.amountMinor > math.MaxInt64/int64(l.quantity) {
		return Money{}, ErrTotalOutOfRange
	}

	return Money{
		amountMinor: l.unitPrice.amountMinor * int64(l.quantity),
		currency:    l.unitPrice.currency,
	}, nil
}
