package domain_test

import (
	"errors"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
)

func TestNewMethod(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  domain.Method
		valid bool
	}{
		{"lowercased", "CARD", "card", true},
		{"trimmed", "  promptpay  ", "promptpay", true},
		{"underscored", "bank_transfer", "bank_transfer", true},
		{"digits after the first character", "truemoney2", "truemoney2", true},
		// The empty method is not a refusal: it means whatever the provider
		// defaults to, which is what a hosted checkout page decides for itself.
		{"empty is the provider's default", "", "", true},
		{"blank is the same as empty", "   ", "", true},
		{"leading underscore", "_card", "", false},
		{"leading digit", "2c2p", "", false},
		{"hyphen", "bank-transfer", "", false},
		{"space inside", "bank transfer", "", false},
		{"too long", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewMethod(tt.in)

			if !tt.valid {
				var invalid domain.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("NewMethod(%q) error = %v, want a ValidationError", tt.in, err)
				}

				return
			}
			if err != nil {
				t.Fatalf("NewMethod(%q) error = %v, want nil", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("NewMethod(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
