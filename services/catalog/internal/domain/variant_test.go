package domain_test

import (
	"strings"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/domain"
)

func TestNewSKU(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  domain.SKU
		valid bool
	}{
		{
			name:  "letters, digits, and hyphens",
			input: "SHIRT-OXF-M",
			want:  "SHIRT-OXF-M",
			valid: true,
		},
		{
			// The schema's CHECK rejects a lowercased SKU, and two rows
			// differing only in case would be two units nobody can tell apart
			// on a label.
			name:  "lowercase is raised",
			input: "shirt-oxf-m",
			want:  "SHIRT-OXF-M",
			valid: true,
		},
		{
			name:  "surrounding space is trimmed",
			input: "  RICE-5KG  ",
			want:  "RICE-5KG",
			valid: true,
		},
		{
			name:  "shortest allowed",
			input: "ABC",
			want:  "ABC",
			valid: true,
		},
		{
			name:  "empty",
			input: "",
		},
		{
			name:  "too short",
			input: "AB",
		},
		{
			name:  "too long",
			input: strings.Repeat("A", 65),
		},
		{
			name:  "leading hyphen",
			input: "-SHIRT",
		},
		{
			// Every one of these breaks something that is not this service: a
			// label, a search box, or a spreadsheet column.
			name:  "inner space",
			input: "SHIRT OXF",
		},
		{
			name:  "underscore",
			input: "SHIRT_OXF",
		},
		{
			name:  "slash",
			input: "SHIRT/OXF",
		},
		{
			name:  "non-ascii",
			input: "เสื้อ-M",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewSKU(tt.input)

			if tt.valid && err != nil {
				t.Fatalf("NewSKU(%q) = %v, want no error", tt.input, err)
			}

			if !tt.valid && err == nil {
				t.Fatalf("NewSKU(%q) = %q, want an error", tt.input, got)
			}

			if got != tt.want {
				t.Errorf("NewSKU(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestVariantAttributes(t *testing.T) {
	tests := []struct {
		name       string
		attributes map[string]string
		valid      bool
	}{
		{
			name:       "lowercase snake case keys",
			attributes: map[string]string{"size": "M", "fabric_weight": "180gsm"},
			valid:      true,
		},
		{
			name:       "none",
			attributes: nil,
			valid:      true,
		},
		{
			name:       "uppercase key",
			attributes: map[string]string{"Size": "M"},
		},
		{
			name:       "key starting with a digit",
			attributes: map[string]string{"1size": "M"},
		},
		{
			name:       "key with a hyphen",
			attributes: map[string]string{"fabric-weight": "180gsm"},
		},
		{
			name:       "empty key",
			attributes: map[string]string{"": "M"},
		},
		{
			name:       "key longer than 32 characters",
			attributes: map[string]string{strings.Repeat("a", 33): "M"},
		},
		{
			// An attribute with no value says nothing, and a client reading it
			// back cannot tell it from one that was never set.
			name:       "empty value",
			attributes: map[string]string{"size": ""},
		},
		{
			name:       "value longer than 128 characters",
			attributes: map[string]string{"size": strings.Repeat("m", 129)},
		},
		{
			name:       "more than 20 entries",
			attributes: manyAttributes(21),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			product := mustDraft(t)

			err := product.AddVariant(mustSKU(t, "SHIRT-OXF-M"), mustPrice(t, 129000, "THB"), tt.attributes)

			if tt.valid && err != nil {
				t.Fatalf("AddVariant(%v) = %v, want no error", tt.attributes, err)
			}

			if !tt.valid {
				if err == nil {
					t.Fatalf("AddVariant(%v) = nil, want an error", tt.attributes)
				}

				var invalid domain.ValidationError
				asValidationError(t, err, &invalid)

				return
			}

			got := product.Variants()[0].Attributes()
			if len(got) != len(tt.attributes) {
				t.Errorf("Attributes() = %v, want %v", got, tt.attributes)
			}
		})
	}
}

func TestVariantAttributesAreCopied(t *testing.T) {
	product := mustDraft(t)
	attributes := map[string]string{"size": "M"}

	if err := product.AddVariant(mustSKU(t, "SHIRT-OXF-M"), mustPrice(t, 129000, "THB"), attributes); err != nil {
		t.Fatalf("AddVariant() = %v, want no error", err)
	}

	// The caller's map is theirs to keep using, and the one a reader gets back
	// is not the aggregate's.
	attributes["size"] = "L"
	product.Variants()[0].Attributes()["size"] = "XL"

	if got := product.Variants()[0].Attributes()["size"]; got != "M" {
		t.Errorf("size = %q, want M", got)
	}
}

func TestVariantWithNoAttributesRoundTripsAsNil(t *testing.T) {
	// Alternating between nil and an empty non-nil map makes a snapshot compare
	// unequal to itself across a save and a load.
	product := mustDraft(t)

	if err := product.AddVariant(mustSKU(t, "RICE-5KG"), mustPrice(t, 25000, "THB"), map[string]string{}); err != nil {
		t.Fatalf("AddVariant() = %v, want no error", err)
	}

	if got := product.Variants()[0].Snapshot().Attributes; got != nil {
		t.Errorf("Attributes = %v, want nil", got)
	}
}

// manyAttributes builds a valid map of n entries, for the bound that is about
// size rather than shape.
func manyAttributes(n int) map[string]string {
	attributes := make(map[string]string, n)
	for i := range n {
		attributes[string(rune('a'+i%26))+strings.Repeat("x", i/26+1)] = "value"
	}

	return attributes
}
