package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
)

const canonical = "3f5b8e2a-1c9d-4a6e-b3f0-2d7c9e4a1b5f"

func TestNewPaymentIDIsCanonical(t *testing.T) {
	id := domain.NewPaymentID()

	parsed, err := domain.ParsePaymentID(id.String())
	if err != nil {
		t.Fatalf("ParsePaymentID(%q) error = %v, want nil", id, err)
	}
	if parsed != id {
		t.Errorf("ParsePaymentID(%q) = %q, want the id back unchanged", id, parsed)
	}
}

func TestParseIdentifiers(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		valid bool
	}{
		{"canonical", canonical, true},
		{"trimmed", "  " + canonical + "  ", true},
		{"empty", "", false},
		{"not a uuid", "not-a-uuid", false},
		// uuid.Parse accepts these, and accepting them here would store a second
		// spelling of one identifier that compares unequal to the first.
		{"urn form", "urn:uuid:" + canonical, false},
		{"brace wrapped", "{" + canonical + "}", false},
		{"unhyphenated", strings.ReplaceAll(canonical, "-", ""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, parse := range []struct {
				name string
				fn   func(string) (string, error)
			}{
				{"ParsePaymentID", func(s string) (string, error) {
					id, err := domain.ParsePaymentID(s)

					return id.String(), err
				}},
				{"ParseOrderID", func(s string) (string, error) {
					id, err := domain.ParseOrderID(s)

					return id.String(), err
				}},
				{"ParseUserID", func(s string) (string, error) {
					id, err := domain.ParseUserID(s)

					return id.String(), err
				}},
			} {
				got, err := parse.fn(tt.in)

				if !tt.valid {
					var invalid domain.ValidationError
					if !errors.As(err, &invalid) {
						t.Errorf("%s(%q) error = %v, want a ValidationError", parse.name, tt.in, err)
					}

					continue
				}
				if err != nil {
					t.Errorf("%s(%q) error = %v, want nil", parse.name, tt.in, err)

					continue
				}
				if got != canonical {
					t.Errorf("%s(%q) = %q, want %q", parse.name, tt.in, got, canonical)
				}
			}
		})
	}
}

// Each identifier names the field the caller used, so a client can point at the
// input it got wrong rather than at "id" for all three.
func TestParseIdentifiersNameTheirOwnField(t *testing.T) {
	for _, tt := range []struct {
		call func() error
		want string
	}{
		{func() error { _, err := domain.ParsePaymentID(""); return err }, "id"},
		{func() error { _, err := domain.ParseOrderID(""); return err }, "order_id"},
		{func() error { _, err := domain.ParseUserID(""); return err }, "user_id"},
		{func() error { _, err := domain.ParseProviderReference(""); return err }, "provider_reference"},
	} {
		err := tt.call()

		var invalid domain.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("error = %v, want a ValidationError", err)
		}
		if invalid.Field != tt.want {
			t.Errorf("Field = %q, want %q", invalid.Field, tt.want)
		}
	}
}

func TestParseProviderReference(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  domain.ProviderReference
		valid bool
	}{
		// Its shape belongs to whoever is collecting the money. A system that
		// insisted on a format would refuse to record a charge it had made.
		{"a provider's own spelling", "pi_3Nk9Xy2eZvKYlo2C0abcdefg", "pi_3Nk9Xy2eZvKYlo2C0abcdefg", true},
		{"trimmed", "  chrg_test_1  ", "chrg_test_1", true},
		{"a uuid is fine too", canonical, domain.ProviderReference(canonical), true},
		{"empty", "", "", false},
		{"blank", "   ", "", false},
		{"too long", strings.Repeat("a", 256), "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.ParseProviderReference(tt.in)

			if !tt.valid {
				var invalid domain.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("ParseProviderReference(%q) error = %v, want a ValidationError", tt.in, err)
				}

				return
			}
			if err != nil {
				t.Fatalf("ParseProviderReference(%q) error = %v, want nil", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseProviderReference(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
