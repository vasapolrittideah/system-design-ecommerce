package domain_test

import (
	"errors"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
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

func TestNewMoney(t *testing.T) {
	thb, err := domain.NewCurrencyCode("THB")
	if err != nil {
		t.Fatalf("NewCurrencyCode() error = %v, want nil", err)
	}

	tests := []struct {
		name        string
		amountMinor int64
		currency    domain.CurrencyCode
		valid       bool
	}{
		{"an amount", 49900, thb, true},
		// Zero is money here even though NewPayment refuses to charge it: this
		// type is also the shape of what a provider reports, and a provider
		// that says it collected nothing is describing a real answer.
		{"zero", 0, thb, true},
		{"negative", -1, thb, false},
		{"no currency", 100, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewMoney(tt.amountMinor, tt.currency)

			if !tt.valid {
				var invalid domain.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("NewMoney() error = %v, want a ValidationError", err)
				}

				return
			}
			if err != nil {
				t.Fatalf("NewMoney() error = %v, want nil", err)
			}
			if got.AmountMinor() != tt.amountMinor || got.Currency() != tt.currency {
				t.Errorf("NewMoney() = %d %s, want %d %s",
					got.AmountMinor(), got.Currency(), tt.amountMinor, tt.currency)
			}
		})
	}
}

// Currency is part of the value, so the same number in two currencies is two
// different amounts. Comparing only the number is how a service ends up
// accepting USD 100 for a THB 100 order.
func TestMoneyEqualComparesTheCurrencyToo(t *testing.T) {
	usd, err := domain.NewCurrencyCode("USD")
	if err != nil {
		t.Fatalf("NewCurrencyCode() error = %v, want nil", err)
	}

	same := thb(t, 49900)
	if !same.Equal(thb(t, 49900)) {
		t.Error("Equal() = false for two identical amounts, want true")
	}
	if same.Equal(thb(t, 49901)) {
		t.Error("Equal() = true for two different amounts, want false")
	}

	other, err := domain.NewMoney(49900, usd)
	if err != nil {
		t.Fatalf("NewMoney() error = %v, want nil", err)
	}
	if same.Equal(other) {
		t.Error("Equal() = true across currencies, want false")
	}
}

func TestMoneyIsZero(t *testing.T) {
	var unset domain.Money
	if !unset.IsZero() {
		t.Error("IsZero() = false on the zero value, want true")
	}

	// Free is not unpriced: the pair is the value, so an amount of zero in a
	// named currency is money.
	if thb(t, 0).IsZero() {
		t.Error("IsZero() = true for THB 0, want false")
	}
}

func TestReconstituteMoneyValidatesNothing(t *testing.T) {
	// A rule tightened afterwards must not make an existing row unreadable, and
	// an attempt that cannot be loaded is one nobody can reconcile.
	got := domain.ReconstituteMoney(-1, "xx")
	if got.AmountMinor() != -1 || got.Currency() != "xx" {
		t.Errorf("ReconstituteMoney() = %d %s, want it stored as given", got.AmountMinor(), got.Currency())
	}
}
