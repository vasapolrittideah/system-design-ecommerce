package domain_test

import (
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/domain"
)

func TestNewCurrencyCode(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  domain.CurrencyCode
		valid bool
	}{
		{
			name:  "uppercase code",
			input: "THB",
			want:  "THB",
			valid: true,
		},
		{
			// The schema's CHECK rejects a lowercased code, so normalising here
			// is what keeps a writer that took the caller's spelling literally
			// from failing at the database instead of at the boundary.
			name:  "lowercase is raised",
			input: "thb",
			want:  "THB",
			valid: true,
		},
		{
			name:  "surrounding space is trimmed",
			input: "  usd\n",
			want:  "USD",
			valid: true,
		},
		{
			// Not on any list this package holds, and deliberately accepted:
			// which currencies the shop sells in is not the domain's to decide.
			name:  "unused but well-formed code",
			input: "ZWL",
			want:  "ZWL",
			valid: true,
		},
		{
			name:  "empty",
			input: "",
		},
		{
			name:  "too short",
			input: "TH",
		},
		{
			name:  "too long",
			input: "THBX",
		},
		{
			name:  "numeric code",
			input: "764",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewCurrencyCode(tt.input)

			if tt.valid && err != nil {
				t.Fatalf("NewCurrencyCode(%q) = %v, want no error", tt.input, err)
			}

			if !tt.valid && err == nil {
				t.Fatalf("NewCurrencyCode(%q) = %q, want an error", tt.input, got)
			}

			if got != tt.want {
				t.Errorf("NewCurrencyCode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewMoney(t *testing.T) {
	tests := []struct {
		name        string
		amountMinor int64
		currency    domain.CurrencyCode
		valid       bool
	}{
		{
			name:        "ordinary price",
			amountMinor: 129000,
			currency:    "THB",
			valid:       true,
		},
		{
			// A giveaway is priced, and priced at nothing. Only an absent
			// currency means unpriced.
			name:        "free",
			amountMinor: 0,
			currency:    "THB",
			valid:       true,
		},
		{
			// The proto declares no such bound, because the same message is the
			// shape of a discount elsewhere. Here it is a price.
			name:        "negative",
			amountMinor: -1,
			currency:    "THB",
		},
		{
			name:        "no currency",
			amountMinor: 100,
			currency:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewMoney(tt.amountMinor, tt.currency)

			if tt.valid && err != nil {
				t.Fatalf("NewMoney(%d, %q) = %v, want no error", tt.amountMinor, tt.currency, err)
			}

			if !tt.valid {
				if err == nil {
					t.Fatalf("NewMoney(%d, %q) = %+v, want an error", tt.amountMinor, tt.currency, got)
				}

				return
			}

			if got.AmountMinor() != tt.amountMinor || got.Currency() != tt.currency {
				t.Errorf("NewMoney(%d, %q) = %d %q", tt.amountMinor, tt.currency, got.AmountMinor(), got.Currency())
			}
		})
	}
}

func TestZeroMoneyIsUnpriced(t *testing.T) {
	// AddVariant and UpdateVariant reject the zero value as a missing price, so
	// a currency that never survived NewCurrencyCode cannot reach the database
	// as a free product.
	free, err := domain.NewMoney(0, "THB")
	if err != nil {
		t.Fatalf("NewMoney(0, THB) = %v, want no error", err)
	}

	if (free == domain.Money{}) {
		t.Error("a price of zero THB equals the zero Money, so free is indistinguishable from unpriced")
	}
}
