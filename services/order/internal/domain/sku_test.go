package domain_test

import (
	"errors"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
)

func TestNewSKU(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  domain.SKU
		valid bool
	}{
		{"uppercased", "mug-01", "MUG-01", true},
		{"trimmed", "  MUG-01  ", "MUG-01", true},
		{"digits only", "12345", "12345", true},
		{"at the minimum", "ABC", "ABC", true},
		{"too short", "AB", "", false},
		{"leading hyphen", "-MUG", "", false},
		{"a space inside", "MUG 01", "", false},
		{"punctuation", "MUG_01", "", false},
		{"empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewSKU(tt.in)

			if !tt.valid {
				var invalid domain.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("NewSKU(%q) error = %v, want a ValidationError", tt.in, err)
				}

				return
			}
			if err != nil {
				t.Fatalf("NewSKU(%q) error = %v, want nil", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("NewSKU(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
