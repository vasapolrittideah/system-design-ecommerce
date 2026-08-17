package domain_test

import (
	"strings"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/domain"
)

func TestNewProductID(t *testing.T) {
	id := domain.NewProductID()

	if _, err := domain.ParseProductID(id.String()); err != nil {
		t.Errorf("NewProductID() produced %q, which does not parse: %v", id, err)
	}

	if id == domain.NewProductID() {
		t.Error("NewProductID() returned the same id twice")
	}
}

func TestParseProductID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  domain.ProductID
		valid bool
	}{
		{
			name:  "canonical form",
			input: "0195c1d2-3f4a-7b8c-9d0e-1f2a3b4c5d6e",
			want:  "0195c1d2-3f4a-7b8c-9d0e-1f2a3b4c5d6e",
			valid: true,
		},
		{
			// PostgreSQL renders a uuid column lowercased, so an id that
			// arrived uppercased has to compare equal to the one that comes
			// back out.
			name:  "uppercase is lowered",
			input: "0195C1D2-3F4A-7B8C-9D0E-1F2A3B4C5D6E",
			want:  "0195c1d2-3f4a-7b8c-9d0e-1f2a3b4c5d6e",
			valid: true,
		},
		{
			name:  "empty",
			input: "",
		},
		{
			// Accepted by uuid.Validate, and would make one row reachable under
			// a second key.
			name:  "braced form",
			input: "{0195c1d2-3f4a-7b8c-9d0e-1f2a3b4c5d6e}",
		},
		{
			name:  "urn form",
			input: "urn:uuid:0195c1d2-3f4a-7b8c-9d0e-1f2a3b4c5d6e",
		},
		{
			name:  "unhyphenated form",
			input: "0195c1d23f4a7b8c9d0e1f2a3b4c5d6e",
		},
		{
			name:  "right length, not a uuid",
			input: strings.Repeat("z", 36),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.ParseProductID(tt.input)

			if tt.valid && err != nil {
				t.Fatalf("ParseProductID(%q) = %v, want no error", tt.input, err)
			}

			if !tt.valid && err == nil {
				t.Fatalf("ParseProductID(%q) = %q, want an error", tt.input, got)
			}

			if got != tt.want {
				t.Errorf("ParseProductID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseVariantID(t *testing.T) {
	id := domain.NewVariantID()

	got, err := domain.ParseVariantID(strings.ToUpper(id.String()))
	if err != nil {
		t.Fatalf("ParseVariantID(%q) = %v, want no error", id, err)
	}

	if got != id {
		t.Errorf("ParseVariantID(%q) = %q, want %q", strings.ToUpper(id.String()), got, id)
	}

	if _, err := domain.ParseVariantID("not-a-uuid"); err == nil {
		t.Error("ParseVariantID(\"not-a-uuid\") = nil, want an error")
	}
}

func TestParsedIDErrorsNameTheirField(t *testing.T) {
	// The field name is what a client reads to know which input it got wrong,
	// and the two ids arrive on one request together.
	tests := map[string]func(string) error{
		"product_id": func(s string) error { _, err := domain.ParseProductID(s); return err },
		"variant_id": func(s string) error { _, err := domain.ParseVariantID(s); return err },
	}

	for field, parse := range tests {
		t.Run(field, func(t *testing.T) {
			var invalid domain.ValidationError
			if err := parse("nope"); !asValidationError(t, err, &invalid) {
				return
			}

			if invalid.Field != field {
				t.Errorf("field = %q, want %q", invalid.Field, field)
			}
		})
	}
}
