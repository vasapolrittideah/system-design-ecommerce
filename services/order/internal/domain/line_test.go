package domain_test

import (
	"errors"
	"math"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
)

func TestNewOrderLineRejectsWhatIsNotALine(t *testing.T) {
	sku, err := domain.NewSKU("MUG-01")
	if err != nil {
		t.Fatalf("NewSKU() error = %v, want nil", err)
	}
	priced := thb(t, 15000)

	tests := []struct {
		name      string
		sku       domain.SKU
		quantity  int
		unitPrice domain.Money
	}{
		{"no sku", "", 1, priced},
		{"zero quantity", sku, 0, priced},
		{"negative quantity", sku, -1, priced},
		{"above the per-line maximum", sku, 10001, priced},
		// A zero Money is "unpriced", not "free": the pair is the value.
		{"no price", sku, 1, domain.Money{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := domain.NewOrderLine(tt.sku, tt.quantity, tt.unitPrice)

			var invalid domain.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("NewOrderLine() error = %v, want a ValidationError", err)
			}
		})
	}
}

func TestSubtotalMultipliesTheAgreedPrice(t *testing.T) {
	l := line(t, "MUG-01", 3, thb(t, 15000))

	subtotal, err := l.Subtotal()
	if err != nil {
		t.Fatalf("Subtotal() error = %v, want nil", err)
	}
	if got := subtotal.AmountMinor(); got != 45000 {
		t.Errorf("subtotal = %d, want %d", got, 45000)
	}
	if got := subtotal.Currency().String(); got != "THB" {
		t.Errorf("subtotal currency = %q, want %q", got, "THB")
	}
}

func TestSubtotalRefusesAProductThatWouldWrap(t *testing.T) {
	l := line(t, "MUG-01", 2, thb(t, math.MaxInt64-1))

	if _, err := l.Subtotal(); !errors.Is(err, domain.ErrTotalOutOfRange) {
		t.Fatalf("Subtotal() error = %v, want ErrTotalOutOfRange", err)
	}
}
