package domain_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/domain"
)

func TestNewCategory(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  domain.Category
		valid bool
	}{
		{
			name:  "single segment",
			input: "clothing",
			want:  "clothing",
			valid: true,
		},
		{
			name:  "nested segments",
			input: "clothing/shirts",
			want:  "clothing/shirts",
			valid: true,
		},
		{
			name:  "hyphenated segment",
			input: "home-and-living/bed-linen",
			want:  "home-and-living/bed-linen",
			valid: true,
		},
		{
			// The schema's CHECK rejects anything but lowercase, and a filter
			// is a plain equality on the stored value.
			name:  "uppercase is lowered",
			input: "Clothing/Shirts",
			want:  "clothing/shirts",
			valid: true,
		},
		{
			// A draft is allowed to exist before anyone has decided where it
			// belongs.
			name:  "empty means uncategorised",
			input: "",
			want:  "",
			valid: true,
		},
		{
			name:  "only whitespace",
			input: "   ",
			want:  "",
			valid: true,
		},
		{
			name:  "leading slash",
			input: "/clothing",
		},
		{
			name:  "trailing slash",
			input: "clothing/",
		},
		{
			name:  "doubled separator",
			input: "clothing//shirts",
		},
		{
			name:  "hyphen next to slash",
			input: "clothing-/shirts",
		},
		{
			name:  "inner space",
			input: "home and living",
		},
		{
			name:  "longer than 64 characters",
			input: strings.Repeat("a", 65),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewCategory(tt.input)

			if tt.valid && err != nil {
				t.Fatalf("NewCategory(%q) = %v, want no error", tt.input, err)
			}

			if !tt.valid && err == nil {
				t.Fatalf("NewCategory(%q) = %q, want an error", tt.input, got)
			}

			if got != tt.want {
				t.Errorf("NewCategory(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewProduct(t *testing.T) {
	tests := []struct {
		name        string
		productName string
		description string
		valid       bool
	}{
		{
			name:        "named and described",
			productName: "Oxford Shirt",
			description: "Button-down, mid weight.",
			valid:       true,
		},
		{
			name:        "no description",
			productName: "Oxford Shirt",
			valid:       true,
		},
		{
			name:        "surrounding space is trimmed",
			productName: "  Oxford Shirt  ",
			valid:       true,
		},
		{
			name:        "no name",
			productName: "",
		},
		{
			name:        "name of only whitespace",
			productName: "   ",
		},
		{
			name:        "name longer than 200 characters",
			productName: strings.Repeat("a", 201),
		},
		{
			name:        "description longer than 4000 characters",
			productName: "Oxford Shirt",
			description: strings.Repeat("a", 4001),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewProduct(tt.productName, tt.description, "clothing")

			if !tt.valid {
				if err == nil {
					t.Fatalf("NewProduct(%q, ...) = %v, want an error", tt.productName, got)
				}

				var invalid domain.ValidationError
				asValidationError(t, err, &invalid)

				return
			}

			if err != nil {
				t.Fatalf("NewProduct(%q, ...) = %v, want no error", tt.productName, err)
			}

			if got.Name() != strings.TrimSpace(tt.productName) {
				t.Errorf("Name() = %q, want %q", got.Name(), strings.TrimSpace(tt.productName))
			}

			// A product is never born on the storefront: everything reaches
			// customers through Publish, which has rules.
			if got.Status() != domain.StatusDraft {
				t.Errorf("Status() = %q, want %q", got.Status(), domain.StatusDraft)
			}

			if len(got.Variants()) != 0 {
				t.Errorf("Variants() = %v, want none", got.Variants())
			}

			if got.Version() != 1 {
				t.Errorf("Version() = %d, want 1", got.Version())
			}
		})
	}
}

func TestAddVariant(t *testing.T) {
	tests := []struct {
		name    string
		product func(t *testing.T) *domain.Product
		sku     domain.SKU
		price   func(t *testing.T) domain.Money
		wantErr error
	}{
		{
			name:    "first variant of a draft",
			product: mustDraft,
			sku:     "SHIRT-OXF-M",
			price:   price(129000, "THB"),
		},
		{
			// A new size arriving is not a reason to take a product off the
			// storefront.
			name:    "another variant of a live product",
			product: mustPublished,
			sku:     "SHIRT-OXF-L",
			price:   price(129000, "THB"),
		},
		{
			name:    "sku already on this product",
			product: mustPublished,
			sku:     "SHIRT-OXF-M",
			price:   price(129000, "THB"),
			wantErr: domain.ErrDuplicateSKU,
		},
		{
			// Summing two currencies produces a number that is a price in
			// neither, and the sum happens in a cart, far from here.
			name:    "price in another currency",
			product: mustPublished,
			sku:     "SHIRT-OXF-L",
			price:   price(4900, "USD"),
			wantErr: domain.ErrCurrencyMismatch,
		},
		{
			name:    "archived product",
			product: mustArchived,
			sku:     "SHIRT-OXF-L",
			price:   price(129000, "THB"),
			wantErr: domain.ErrProductArchived,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			product := tt.product(t)
			before := len(product.Variants())

			err := product.AddVariant(tt.sku, tt.price(t), map[string]string{"size": "M"})

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("AddVariant() = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr != nil {
				if got := len(product.Variants()); got != before {
					t.Errorf("Variants() has %d entries after a refused add, want %d", got, before)
				}

				return
			}

			added := product.Variants()[before]
			if added.SKU() != tt.sku {
				t.Errorf("SKU() = %q, want %q", added.SKU(), tt.sku)
			}

			if added.ProductID() != product.ID() {
				t.Errorf("ProductID() = %q, want %q", added.ProductID(), product.ID())
			}

			// The zero time is what tells the repository this row has never
			// been written, and so must be inserted rather than updated.
			if !added.CreatedAt().IsZero() {
				t.Errorf("CreatedAt() = %v, want the zero time", added.CreatedAt())
			}

			if added.Version() != 1 {
				t.Errorf("Version() = %d, want 1", added.Version())
			}
		})
	}
}

func TestAddVariantRequiresSKUAndPrice(t *testing.T) {
	tests := []struct {
		name  string
		sku   domain.SKU
		price domain.Money
	}{
		{name: "no sku", price: mustPrice(t, 129000, "THB")},
		{name: "no price", sku: "SHIRT-OXF-M"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			product := mustDraft(t)

			var invalid domain.ValidationError
			asValidationError(t, product.AddVariant(tt.sku, tt.price, nil), &invalid)
		})
	}
}

func TestUpdateVariant(t *testing.T) {
	t.Run("reprices the variant it names", func(t *testing.T) {
		product := mustPublished(t)
		id := product.Variants()[0].ID()

		if err := product.UpdateVariant(id, mustPrice(t, 99000, "THB"), map[string]string{"size": "M"}); err != nil {
			t.Fatalf("UpdateVariant() = %v, want no error", err)
		}

		if got := product.Variants()[0].Price().AmountMinor(); got != 99000 {
			t.Errorf("AmountMinor() = %d, want 99000", got)
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		product := mustPublished(t)

		// Including a variant that exists under another product, which this
		// aggregate cannot see and must not appear to have modified.
		err := product.UpdateVariant(domain.NewVariantID(), mustPrice(t, 99000, "THB"), nil)

		if !errors.Is(err, domain.ErrVariantNotFound) {
			t.Errorf("UpdateVariant() = %v, want %v", err, domain.ErrVariantNotFound)
		}
	})

	t.Run("the only variant may change currency", func(t *testing.T) {
		// There is nothing for it to disagree with, and refusing would make
		// repricing a one-variant product into deleting and recreating it.
		product := mustPublished(t)
		id := product.Variants()[0].ID()

		if err := product.UpdateVariant(id, mustPrice(t, 4900, "USD"), nil); err != nil {
			t.Fatalf("UpdateVariant() = %v, want no error", err)
		}

		if got := product.Variants()[0].Price().Currency(); got != "USD" {
			t.Errorf("Currency() = %q, want USD", got)
		}
	})

	t.Run("one of several may not", func(t *testing.T) {
		product := mustPublished(t)
		if err := product.AddVariant("SHIRT-OXF-L", mustPrice(t, 129000, "THB"), nil); err != nil {
			t.Fatalf("AddVariant() = %v, want no error", err)
		}

		err := product.UpdateVariant(product.Variants()[0].ID(), mustPrice(t, 4900, "USD"), nil)

		if !errors.Is(err, domain.ErrCurrencyMismatch) {
			t.Errorf("UpdateVariant() = %v, want %v", err, domain.ErrCurrencyMismatch)
		}
	})

	t.Run("archived product", func(t *testing.T) {
		product := mustPublished(t)
		id := product.Variants()[0].ID()
		product.Archive()

		err := product.UpdateVariant(id, mustPrice(t, 99000, "THB"), nil)

		if !errors.Is(err, domain.ErrProductArchived) {
			t.Errorf("UpdateVariant() = %v, want %v", err, domain.ErrProductArchived)
		}
	})
}

func TestUpdateDetails(t *testing.T) {
	t.Run("replaces every descriptive field", func(t *testing.T) {
		product := mustDraft(t)

		if err := product.UpdateDetails("Rice", "", "food/grains"); err != nil {
			t.Fatalf("UpdateDetails() = %v, want no error", err)
		}

		if product.Name() != "Rice" || product.Description() != "" || product.Category() != "food/grains" {
			t.Errorf("product = %q, %q, %q", product.Name(), product.Description(), product.Category())
		}
	})

	t.Run("keeps the product unchanged when the name is refused", func(t *testing.T) {
		product := mustDraft(t)
		name := product.Name()

		if err := product.UpdateDetails("", "new copy", "food"); err == nil {
			t.Fatal("UpdateDetails() = nil, want an error")
		}

		if product.Name() != name || product.Description() == "new copy" {
			t.Errorf("product changed to %q, %q after a refused update", product.Name(), product.Description())
		}
	})

	t.Run("archived product", func(t *testing.T) {
		product := mustArchived(t)

		if err := product.UpdateDetails("Rice", "", "food"); !errors.Is(err, domain.ErrProductArchived) {
			t.Errorf("UpdateDetails() = %v, want %v", err, domain.ErrProductArchived)
		}
	})
}

func TestPublish(t *testing.T) {
	t.Run("draft with a variant", func(t *testing.T) {
		product := mustDraft(t)
		if err := product.AddVariant("SHIRT-OXF-M", mustPrice(t, 129000, "THB"), nil); err != nil {
			t.Fatalf("AddVariant() = %v, want no error", err)
		}

		if err := product.Publish(); err != nil {
			t.Fatalf("Publish() = %v, want no error", err)
		}

		if product.Status() != domain.StatusActive {
			t.Errorf("Status() = %q, want %q", product.Status(), domain.StatusActive)
		}
	})

	t.Run("draft with no variants", func(t *testing.T) {
		// Nothing on the page could be bought, and the shopper would find that
		// out only after clicking.
		product := mustDraft(t)

		if err := product.Publish(); !errors.Is(err, domain.ErrProductHasNoVariants) {
			t.Errorf("Publish() = %v, want %v", err, domain.ErrProductHasNoVariants)
		}

		if product.Status() != domain.StatusDraft {
			t.Errorf("Status() = %q, want %q", product.Status(), domain.StatusDraft)
		}
	})

	t.Run("already active", func(t *testing.T) {
		// A state the caller wants to reach, not a change they are making: an
		// admin clicking twice has made no mistake.
		product := mustPublished(t)

		if err := product.Publish(); err != nil {
			t.Fatalf("Publish() = %v, want no error", err)
		}

		if product.Status() != domain.StatusActive {
			t.Errorf("Status() = %q, want %q", product.Status(), domain.StatusActive)
		}
	})

	t.Run("archived", func(t *testing.T) {
		product := mustArchived(t)

		if err := product.Publish(); !errors.Is(err, domain.ErrProductArchived) {
			t.Errorf("Publish() = %v, want %v", err, domain.ErrProductArchived)
		}
	})
}

func TestArchive(t *testing.T) {
	tests := map[string]func(t *testing.T) *domain.Product{
		"draft":    mustDraft,
		"active":   mustPublished,
		"archived": mustArchived,
	}

	for name, build := range tests {
		t.Run(name, func(t *testing.T) {
			product := build(t)

			product.Archive()

			if product.Status() != domain.StatusArchived {
				t.Errorf("Status() = %q, want %q", product.Status(), domain.StatusArchived)
			}
		})
	}
}

func TestVariantsSliceIsCopied(t *testing.T) {
	product := mustPublished(t)

	handed := product.Variants()
	handed[0] = nil

	if got := product.Variants(); len(got) != 1 || got[0] == nil {
		t.Errorf("Variants() = %v after a caller wrote to the slice it was handed", got)
	}
}

func TestProductSentinelKinds(t *testing.T) {
	// Pinned to the constants rather than the literals the sentinels carry:
	// domain cannot import errorx, so a kind that stops matching would
	// otherwise resolve to Internal with nothing reporting it.
	tests := map[error]errorx.Kind{
		domain.ErrProductArchived:      errorx.KindConflict,
		domain.ErrProductHasNoVariants: errorx.KindConflict,
		domain.ErrDuplicateSKU:         errorx.KindConflict,
		domain.ErrCurrencyMismatch:     errorx.KindConflict,
		domain.ErrVariantNotFound:      errorx.KindNotFound,

		// Not a sentinel, and the one kind that says the caller can fix the
		// request by changing it.
		domain.ValidationError{Field: "name", Message: "is required"}: errorx.KindInvalidInput,
	}

	for err, want := range tests {
		t.Run(err.Error(), func(t *testing.T) {
			var kinder interface{ ErrorKind() string }
			if !errors.As(err, &kinder) {
				t.Fatalf("%v does not declare a kind", err)
			}

			if got := kinder.ErrorKind(); got != string(want) {
				t.Errorf("%v declares kind %q, want %q", err, got, want)
			}
		})
	}
}

func TestReconstituteProductRoundTrips(t *testing.T) {
	product := mustPublished(t)

	got := domain.ReconstituteProduct(product.Snapshot()).Snapshot()

	if !reflect.DeepEqual(got, product.Snapshot()) {
		t.Errorf("Snapshot() = %+v, want %+v", got, product.Snapshot())
	}
}

func TestReconstituteValidatesNothing(t *testing.T) {
	// A rule tightened afterwards must not make existing products unreadable —
	// including the currency rule, which a product priced before it existed may
	// well break.
	product := domain.ReconstituteProduct(domain.ProductSnapshot{
		ID:     domain.NewProductID(),
		Name:   "",
		Status: "something nobody defined",
		Variants: []domain.VariantSnapshot{
			{ID: domain.NewVariantID(), SKU: "lowercase-sku", Price: mustPrice(t, 1, "THB")},
			{ID: domain.NewVariantID(), SKU: "another", Price: mustPrice(t, 1, "USD")},
		},
	})

	if len(product.Variants()) != 2 {
		t.Errorf("Variants() = %v, want both", product.Variants())
	}
}

// mustDraft builds an unpublished product with no variants.
func mustDraft(t *testing.T) *domain.Product {
	t.Helper()

	product, err := domain.NewProduct("Oxford Shirt", "Button-down, mid weight.", "clothing/shirts")
	if err != nil {
		t.Fatalf("NewProduct() = %v, want no error", err)
	}

	return product
}

// mustPublished builds a live product with exactly one variant, priced in THB.
func mustPublished(t *testing.T) *domain.Product {
	t.Helper()

	product := mustDraft(t)

	if err := product.AddVariant("SHIRT-OXF-M", mustPrice(t, 129000, "THB"), map[string]string{"size": "M"}); err != nil {
		t.Fatalf("AddVariant() = %v, want no error", err)
	}

	if err := product.Publish(); err != nil {
		t.Fatalf("Publish() = %v, want no error", err)
	}

	return product
}

// mustArchived builds a product that has been withdrawn from sale.
func mustArchived(t *testing.T) *domain.Product {
	t.Helper()

	product := mustPublished(t)
	product.Archive()

	return product
}

// mustSKU builds a SKU the test expects to be valid.
func mustSKU(t *testing.T, s string) domain.SKU {
	t.Helper()

	sku, err := domain.NewSKU(s)
	if err != nil {
		t.Fatalf("NewSKU(%q) = %v, want no error", s, err)
	}

	return sku
}

// mustPrice builds a price the test expects to be valid.
func mustPrice(t *testing.T, amountMinor int64, currency domain.CurrencyCode) domain.Money {
	t.Helper()

	money, err := domain.NewMoney(amountMinor, currency)
	if err != nil {
		t.Fatalf("NewMoney(%d, %q) = %v, want no error", amountMinor, currency, err)
	}

	return money
}

// price defers mustPrice, for table rows that need one before a *testing.T for
// the subtest exists.
func price(amountMinor int64, currency domain.CurrencyCode) func(t *testing.T) domain.Money {
	return func(t *testing.T) domain.Money {
		t.Helper()

		return mustPrice(t, amountMinor, currency)
	}
}

// asValidationError reports whether err is the kind a caller can fix by
// changing the request, and fails the test when it is not.
func asValidationError(t *testing.T, err error, target *domain.ValidationError) bool {
	t.Helper()

	if !errors.As(err, target) {
		t.Errorf("%v is not a ValidationError", err)

		return false
	}

	return true
}
