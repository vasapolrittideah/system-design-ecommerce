package domain_test

import (
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
)

func TestNewQuantity(t *testing.T) {
	tests := []struct {
		name    string
		input   int32
		want    domain.Quantity
		wantErr bool
	}{
		// Zero is a tracked SKU nobody has delivered yet, which is the state
		// that keeps an order from being taken for it.
		{name: "none", input: 0, want: 0},
		{name: "some", input: 7, want: 7},
		{name: "the ceiling", input: 1_000_000, want: 1_000_000},
		{name: "over the ceiling", input: 1_000_001, wantErr: true},
		{name: "negative", input: -1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewQuantity("available", tt.input)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("NewQuantity(%d) error = nil, want a validation error", tt.input)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewQuantity(%d) error = %v, want nil", tt.input, err)
			}

			if got != tt.want {
				t.Errorf("NewQuantity(%d) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewAdjustment(t *testing.T) {
	tests := []struct {
		name    string
		input   int32
		want    domain.Adjustment
		wantErr bool
	}{
		{name: "a delivery", input: 24, want: 24},
		{name: "a breakage", input: -2, want: -2},
		// An adjustment of nothing is a caller that computed the wrong number,
		// and answering "done" hides that until somebody counts a shelf.
		{name: "no change at all", input: 0, wantErr: true},
		{name: "over the ceiling", input: 1_000_001, wantErr: true},
		{name: "under the floor", input: -1_000_001, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewAdjustment(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("NewAdjustment(%d) error = nil, want a validation error", tt.input)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewAdjustment(%d) error = %v, want nil", tt.input, err)
			}

			if got != tt.want {
				t.Errorf("NewAdjustment(%d) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewSKU(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    domain.SKU
		wantErr bool
	}{
		{name: "already uppercase", input: "SHIRT-OXF-M", want: "SHIRT-OXF-M"},
		// Normalised rather than refused: this string is typed into search boxes
		// and pasted out of spreadsheets, and case is not what distinguishes two
		// products.
		{name: "lowercase", input: "shirt-oxf-m", want: "SHIRT-OXF-M"},
		{name: "padded", input: "  SHIRT-M  ", want: "SHIRT-M"},
		{name: "digits only", input: "12345", want: "12345"},
		{name: "too short", input: "AB", wantErr: true},
		{name: "leading hyphen", input: "-SHIRT", wantErr: true},
		{name: "a space inside", input: "SHIRT M", wantErr: true},
		{name: "an underscore", input: "SHIRT_M", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewSKU(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("NewSKU(%q) error = nil, want a validation error", tt.input)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewSKU(%q) error = %v, want nil", tt.input, err)
			}

			if got != tt.want {
				t.Errorf("NewSKU(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseOrderID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "canonical", input: "6f1c0a4e-2b8d-4c1a-9f3e-5d7b8a2c4e60"},
		{name: "uppercase", input: "6F1C0A4E-2B8D-4C1A-9F3E-5D7B8A2C4E60"},
		// uuid.Validate accepts these spellings, which would make one order
		// reachable under several different keys.
		{name: "braced", input: "{6f1c0a4e-2b8d-4c1a-9f3e-5d7b8a2c4e60}", wantErr: true},
		{name: "unhyphenated", input: "6f1c0a4e2b8d4c1a9f3e5d7b8a2c4e60", wantErr: true},
		{name: "not a uuid", input: "order-1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.ParseOrderID(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseOrderID(%q) error = nil, want a validation error", tt.input)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseOrderID(%q) error = %v, want nil", tt.input, err)
			}

			// Lowercased, because that is how PostgreSQL renders a uuid column
			// coming back out.
			if got.String() != "6f1c0a4e-2b8d-4c1a-9f3e-5d7b8a2c4e60" {
				t.Errorf("ParseOrderID(%q) = %q, want the canonical lowercase form", tt.input, got)
			}
		})
	}
}
