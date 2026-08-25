package domain_test

import (
	"errors"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
)

func TestNewCurrencyCode(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  domain.CurrencyCode
		valid bool
	}{
		{"uppercased", "thb", "THB", true},
		{"trimmed", "  usd  ", "USD", true},
		{"already canonical", "JPY", "JPY", true},
		{"too short", "TH", "", false},
		{"too long", "THBB", "", false},
		{"digits", "TH1", "", false},
		{"empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewCurrencyCode(tt.in)

			if tt.valid && err != nil {
				t.Fatalf("NewCurrencyCode(%q) error = %v, want nil", tt.in, err)
			}
			if !tt.valid {
				var invalid domain.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("NewCurrencyCode(%q) error = %v, want a ValidationError", tt.in, err)
				}

				return
			}
			if got != tt.want {
				t.Errorf("NewCurrencyCode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewMoneyRefusesWhatIsNotAnAmount(t *testing.T) {
	currency, err := domain.NewCurrencyCode("THB")
	if err != nil {
		t.Fatalf("NewCurrencyCode() error = %v, want nil", err)
	}

	tests := []struct {
		name        string
		amountMinor int64
		currency    domain.CurrencyCode
	}{
		// Nothing an order holds is negative, though the shared proto message
		// allows it — that one is also the shape of a discount and a refund.
		{"negative", -1, currency},
		{"no currency", 1000, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := domain.NewMoney(tt.amountMinor, tt.currency)

			var invalid domain.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("NewMoney() error = %v, want a ValidationError", err)
			}
		})
	}
}

func TestZeroMoneyIsUnpricedRatherThanFree(t *testing.T) {
	currency, err := domain.NewCurrencyCode("THB")
	if err != nil {
		t.Fatalf("NewCurrencyCode() error = %v, want nil", err)
	}

	free, err := domain.NewMoney(0, currency)
	if err != nil {
		t.Fatalf("NewMoney() error = %v, want nil", err)
	}

	if (domain.Money{}).IsZero() != true {
		t.Error("the zero Money reports IsZero() = false, want true")
	}
	// Zero baht is a price. Only the pair being absent is "unpriced".
	if free.IsZero() {
		t.Error("zero THB reports IsZero() = true, want false")
	}
}

func TestReconstituteMoneyValidatesNothing(t *testing.T) {
	// A rule tightened afterwards must not make an existing order unreadable.
	money := domain.ReconstituteMoney(-1, "xx")

	if got := money.AmountMinor(); got != -1 {
		t.Errorf("amount = %d, want %d", got, -1)
	}
	if got := money.Currency(); got != "xx" {
		t.Errorf("currency = %q, want %q", got, "xx")
	}
}
