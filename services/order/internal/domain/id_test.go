package domain_test

import (
	"errors"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
)

func TestParseOrderID(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		valid bool
	}{
		{"canonical", "01234567-89ab-cdef-0123-456789abcdef", true},
		// uuid.Parse accepts these, and storing one would be a second spelling
		// of the same identifier that compares unequal to the first.
		{"urn form", "urn:uuid:01234567-89ab-cdef-0123-456789abcdef", false},
		{"brace wrapped", "{01234567-89ab-cdef-0123-456789abcdef}", false},
		{"unhyphenated", "0123456789abcdef0123456789abcdef", false},
		{"not a uuid", "order-1", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.ParseOrderID(tt.in)

			if !tt.valid {
				var invalid domain.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("ParseOrderID(%q) error = %v, want a ValidationError", tt.in, err)
				}

				return
			}
			if err != nil {
				t.Fatalf("ParseOrderID(%q) error = %v, want nil", tt.in, err)
			}
			if got.String() != tt.in {
				t.Errorf("ParseOrderID(%q) = %q, want %q", tt.in, got, tt.in)
			}
		})
	}
}

func TestNewOrderIDIsCanonicalAndUnique(t *testing.T) {
	first := domain.NewOrderID()

	if _, err := domain.ParseOrderID(first.String()); err != nil {
		t.Errorf("NewOrderID() produced %q, which ParseOrderID refuses: %v", first, err)
	}
	if second := domain.NewOrderID(); first == second {
		t.Error("two calls to NewOrderID() returned the same id")
	}
}
