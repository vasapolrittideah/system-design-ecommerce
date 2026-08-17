package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres/postgrestest"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/adapter/out/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/port/out"
)

// migrationsDir is the schema. The test applies the same files goose runs in
// the cluster rather than a copy kept next to it, because a copy is a second
// schema that drifts and reports nothing when it does.
const migrationsDir = "../../../../db/migrations"

// setup gives each test its own database with the migrations applied.
//
// A real PostgreSQL, never a fake driver: a UNIQUE violation carrying a
// constraint name, a jsonb column round-tripping, a row comparison paging over
// tied timestamps, and DEFAULT now() firing are the server's behaviour and not
// a mock's.
func setup(t *testing.T) (*pgxpool.Pool, *adapter.ProductRepository) {
	t.Helper()

	pool := postgrestest.New(t, upMigrations(t)...)

	return pool, adapter.NewProductRepository(pool)
}

// upMigrations returns the Up half of every migration, in order. The Down half
// is cut away: applied together they would create the schema and immediately
// drop it.
func upMigrations(t *testing.T) []string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}

	if len(paths) == 0 {
		t.Fatalf("no migrations found in %s", migrationsDir)
	}

	statements := make([]string, 0, len(paths))

	for _, path := range paths {
		content, err := os.ReadFile(path) //nolint:gosec // a path this test built
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		up, _, _ := strings.Cut(string(content), "-- +goose Down")
		statements = append(statements, up)
	}

	return statements
}

func TestCreateAndFindByID(t *testing.T) {
	ctx := context.Background()
	_, products := setup(t)

	created, err := products.Create(ctx, newProduct(t, "Oxford Shirt", "clothing/shirts",
		variant{sku: "SHIRT-OXF-M", amount: 129000, attributes: map[string]string{"size": "M"}},
		variant{sku: "SHIRT-OXF-L", amount: 129000, attributes: map[string]string{"size": "L"}},
	))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	// The timestamps are the database's, and the aggregate had none before the
	// insert returned.
	if created.CreatedAt().IsZero() || created.UpdatedAt().IsZero() {
		t.Errorf("Create() timestamps = %v/%v, want the values DEFAULT now() supplied",
			created.CreatedAt(), created.UpdatedAt())
	}

	if created.Version() != 1 {
		t.Errorf("Create() version = %d, want 1", created.Version())
	}

	found, err := products.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}

	if len(found.Variants()) != 2 {
		t.Fatalf("FindByID() returned %d variants, want 2", len(found.Variants()))
	}

	if got := found.Variants()[0].Attributes()["size"]; got != "M" {
		t.Errorf("FindByID() first variant size = %q, want M", got)
	}

	if got := found.Status(); got != domain.StatusDraft {
		t.Errorf("FindByID() status = %q, want %q", got, domain.StatusDraft)
	}

	if got := found.Variants()[0].Price().Currency(); got != "THB" {
		t.Errorf("FindByID() currency = %q, want THB", got)
	}
}

func TestCreateRejectsSKUUsedByAnotherProduct(t *testing.T) {
	// Uniqueness across the service is the constraint's to enforce: no
	// aggregate can see the rows it would have to check.
	ctx := context.Background()
	_, products := setup(t)

	if _, err := products.Create(ctx, newProduct(t, "Oxford Shirt", "clothing",
		variant{sku: "SHIRT-OXF-M", amount: 129000})); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	_, err := products.Create(ctx, newProduct(t, "Poplin Shirt", "clothing",
		variant{sku: "SHIRT-OXF-M", amount: 99000}))
	if err == nil {
		t.Fatal("Create() error = nil, want a conflict")
	}

	if got := errorx.KindOf(err); got != errorx.KindConflict {
		t.Errorf("KindOf() = %q, want %q", got, errorx.KindConflict)
	}

	if got := errorx.Reason(err); got != "SKU_ALREADY_EXISTS" {
		t.Errorf("Reason() = %q, want SKU_ALREADY_EXISTS", got)
	}

	// The client fixing this needs to know which SKU of a batch collided.
	if got := errorx.Metadata(err)["sku"]; got != "SHIRT-OXF-M" {
		t.Errorf("Metadata()[sku] = %q, want SHIRT-OXF-M", got)
	}
}

func TestUpdateWritesAddedAndChangedVariants(t *testing.T) {
	ctx := context.Background()
	_, products := setup(t)

	created, err := products.Create(ctx, newProduct(t, "Oxford Shirt", "clothing",
		variant{sku: "SHIRT-OXF-M", amount: 129000}))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	loaded, err := products.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}

	if err := loaded.AddVariant("SHIRT-OXF-L", price(t, 139000), nil); err != nil {
		t.Fatalf("AddVariant() error = %v, want nil", err)
	}

	if err := loaded.UpdateVariant(loaded.Variants()[0].ID(), price(t, 99000), map[string]string{"size": "M"}); err != nil {
		t.Fatalf("UpdateVariant() error = %v, want nil", err)
	}

	if err := loaded.Publish(); err != nil {
		t.Fatalf("Publish() error = %v, want nil", err)
	}

	updated, err := products.Update(ctx, loaded)
	if err != nil {
		t.Fatalf("Update() error = %v, want nil", err)
	}

	// The version guards the whole aggregate, which is what makes two
	// concurrent writers that both added a variant serialise.
	if updated.Version() != 2 {
		t.Errorf("Update() version = %d, want 2", updated.Version())
	}

	found, err := products.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}

	if got := found.Status(); got != domain.StatusActive {
		t.Errorf("FindByID() status = %q, want %q", got, domain.StatusActive)
	}

	if len(found.Variants()) != 2 {
		t.Fatalf("FindByID() returned %d variants, want 2", len(found.Variants()))
	}

	if got := found.Variants()[0].Price().AmountMinor(); got != 99000 {
		t.Errorf("FindByID() repriced variant = %d, want 99000", got)
	}

	// The variant that was inserted by this update carries the timestamps only
	// a stored row has, which is what stops the next Update from inserting it
	// again.
	if found.Variants()[1].CreatedAt().IsZero() {
		t.Error("FindByID() added variant has no created_at, want the database's")
	}
}

func TestUpdateRefusesStaleVersion(t *testing.T) {
	// Two readers, one writer each: the second carries a version the first has
	// already moved, and losing is the point of the lock.
	ctx := context.Background()
	_, products := setup(t)

	created, err := products.Create(ctx, newProduct(t, "Oxford Shirt", "clothing",
		variant{sku: "SHIRT-OXF-M", amount: 129000}))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	first, err := products.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}

	second, err := products.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}

	if err := first.UpdateDetails("Oxford Shirt", "first", "clothing"); err != nil {
		t.Fatalf("UpdateDetails() error = %v, want nil", err)
	}

	if _, err := products.Update(ctx, first); err != nil {
		t.Fatalf("Update() error = %v, want nil", err)
	}

	if err := second.UpdateDetails("Oxford Shirt", "second", "clothing"); err != nil {
		t.Fatalf("UpdateDetails() error = %v, want nil", err)
	}

	_, err = products.Update(ctx, second)
	if err == nil {
		t.Fatal("Update() error = nil, want a conflict")
	}

	if got := errorx.KindOf(err); got != errorx.KindConflict {
		t.Errorf("KindOf() = %q, want %q", got, errorx.KindConflict)
	}

	if got := errorx.Reason(err); got != "PRODUCT_MODIFIED" {
		t.Errorf("Reason() = %q, want PRODUCT_MODIFIED", got)
	}

	found, err := products.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}

	if found.Description() != "first" {
		t.Errorf("description = %q, want the winner's", found.Description())
	}
}

func TestFindByIDNotFound(t *testing.T) {
	ctx := context.Background()
	_, products := setup(t)

	_, err := products.FindByID(ctx, domain.NewProductID())

	if got := errorx.KindOf(err); got != errorx.KindNotFound {
		t.Errorf("KindOf() = %q, want %q", got, errorx.KindNotFound)
	}

	if got := errorx.Reason(err); got != "PRODUCT_NOT_FOUND" {
		t.Errorf("Reason() = %q, want PRODUCT_NOT_FOUND", got)
	}
}

func TestFindByIDsSkipsWhatIsMissing(t *testing.T) {
	// This is the read a BFF fans out to fill a screen, and one product that
	// has since been removed must not fail the screen.
	ctx := context.Background()
	_, products := setup(t)

	created, err := products.Create(ctx, newProduct(t, "Oxford Shirt", "clothing",
		variant{sku: "SHIRT-OXF-M", amount: 129000}))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	found, err := products.FindByIDs(ctx, []domain.ProductID{created.ID(), domain.NewProductID()})
	if err != nil {
		t.Fatalf("FindByIDs() error = %v, want nil", err)
	}

	if len(found) != 1 {
		t.Fatalf("FindByIDs() returned %d products, want 1", len(found))
	}

	if len(found[0].Variants()) != 1 {
		t.Errorf("FindByIDs() returned %d variants, want 1", len(found[0].Variants()))
	}
}

func TestListPagesOverTiedTimestamps(t *testing.T) {
	// Five products written in one transaction share a created_at to the
	// microsecond, which is the case a cursor on the timestamp alone drops or
	// repeats rows in. They are inserted this way on purpose.
	ctx := context.Background()
	pool, products := setup(t)
	transactions := txmanager.New(pool)

	ids := make(map[domain.ProductID]bool, 5)

	if err := transactions.Do(ctx, func(ctx context.Context) error {
		for _, sku := range []domain.SKU{"SKU-A", "SKU-B", "SKU-C", "SKU-D", "SKU-E"} {
			product := newProduct(t, "Oxford Shirt", "clothing", variant{sku: sku, amount: 129000})
			if err := product.Publish(); err != nil {
				return err
			}

			created, err := products.Create(ctx, product)
			if err != nil {
				return err
			}

			ids[created.ID()] = false
		}

		return nil
	}); err != nil {
		t.Fatalf("seed error = %v, want nil", err)
	}

	var cursor *out.ProductCursor

	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("listing did not terminate, want the cursor to advance every page")
		}

		page, err := products.List(ctx, out.ProductFilter{
			Status: domain.StatusActive,
			After:  cursor,
			Limit:  2,
		})
		if err != nil {
			t.Fatalf("List() error = %v, want nil", err)
		}

		if len(page) == 0 {
			break
		}

		for _, product := range page {
			seen, known := ids[product.ID()]
			if !known {
				t.Fatalf("List() returned %s, which was never created", product.ID())
			}

			if seen {
				t.Fatalf("List() returned %s twice across pages", product.ID())
			}

			ids[product.ID()] = true
		}

		last := page[len(page)-1]
		cursor = &out.ProductCursor{CreatedAt: last.CreatedAt(), ID: last.ID()}
	}

	for id, seen := range ids {
		if !seen {
			t.Errorf("List() never returned %s", id)
		}
	}
}

func TestListFilters(t *testing.T) {
	ctx := context.Background()
	_, products := setup(t)

	live := newProduct(t, "Oxford Shirt", "clothing/shirts", variant{sku: "SHIRT-OXF-M", amount: 129000})
	if err := live.Publish(); err != nil {
		t.Fatalf("Publish() error = %v, want nil", err)
	}

	if _, err := products.Create(ctx, live); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	other := newProduct(t, "Jasmine Rice", "food/grains", variant{sku: "RICE-5KG", amount: 25000})
	if err := other.Publish(); err != nil {
		t.Fatalf("Publish() error = %v, want nil", err)
	}

	if _, err := products.Create(ctx, other); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if _, err := products.Create(ctx, newProduct(t, "Unfinished", "clothing/shirts",
		variant{sku: "SHIRT-DRAFT", amount: 1})); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	tests := []struct {
		name   string
		filter out.ProductFilter
		want   int
	}{
		{
			name:   "everything live",
			filter: out.ProductFilter{Status: domain.StatusActive, Limit: 10},
			want:   2,
		},
		{
			name:   "one category",
			filter: out.ProductFilter{Status: domain.StatusActive, Category: "clothing/shirts", Limit: 10},
			want:   1,
		},
		{
			// The draft shares a category with the live shirt, and a storefront
			// asking for what it can sell must not be handed it.
			name:   "drafts are their own listing",
			filter: out.ProductFilter{Status: domain.StatusDraft, Category: "clothing/shirts", Limit: 10},
			want:   1,
		},
		{
			name:   "a category nobody used",
			filter: out.ProductFilter{Status: domain.StatusActive, Category: "toys", Limit: 10},
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := products.List(ctx, tt.filter)
			if err != nil {
				t.Fatalf("List() error = %v, want nil", err)
			}

			if len(page) != tt.want {
				t.Errorf("List() returned %d products, want %d", len(page), tt.want)
			}
		})
	}
}

func TestAttributesRoundTrip(t *testing.T) {
	ctx := context.Background()
	_, products := setup(t)

	created, err := products.Create(ctx, newProduct(t, "Oxford Shirt", "clothing",
		variant{sku: "SHIRT-OXF-M", amount: 129000, attributes: map[string]string{"size": "M", "fit": "slim"}},
		variant{sku: "SHIRT-OXF-L", amount: 129000},
	))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	found, err := products.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}

	if got := found.Variants()[0].Attributes(); len(got) != 2 || got["fit"] != "slim" {
		t.Errorf("Attributes() = %v, want both entries", got)
	}

	// Empty comes back nil rather than an empty map, so an aggregate loaded
	// from a row equals the one that was written.
	if got := found.Variants()[1].Snapshot().Attributes; got != nil {
		t.Errorf("Attributes = %v, want nil", got)
	}
}

func TestCreateRollsBackWithTheTransaction(t *testing.T) {
	// The product and its variants are several statements describing one
	// aggregate. Without the transaction the caller opens, a failure after the
	// first insert would leave a product nobody can buy.
	ctx := context.Background()
	pool, products := setup(t)
	transactions := txmanager.New(pool)

	product := newProduct(t, "Oxford Shirt", "clothing", variant{sku: "SHIRT-OXF-M", amount: 129000})

	err := transactions.Do(ctx, func(ctx context.Context) error {
		if _, err := products.Create(ctx, product); err != nil {
			return err
		}

		// The second product reuses the SKU, which the constraint refuses.
		_, err := products.Create(ctx, newProduct(t, "Poplin Shirt", "clothing",
			variant{sku: "SHIRT-OXF-M", amount: 99000}))

		return err
	})
	if err == nil {
		t.Fatal("Do() error = nil, want the conflict to propagate")
	}

	if _, err := products.FindByID(ctx, product.ID()); errorx.KindOf(err) != errorx.KindNotFound {
		t.Errorf("FindByID() after rollback = %v, want not found", err)
	}
}

// variant is what a test says about a sellable unit, before the domain types
// are built from it.
type variant struct {
	sku        domain.SKU
	amount     int64
	attributes map[string]string
}

// newProduct builds a draft with the variants given, priced in THB.
func newProduct(t *testing.T, name string, category domain.Category, variants ...variant) *domain.Product {
	t.Helper()

	product, err := domain.NewProduct(name, "", category)
	if err != nil {
		t.Fatalf("NewProduct() error = %v", err)
	}

	for _, v := range variants {
		if err := product.AddVariant(v.sku, price(t, v.amount), v.attributes); err != nil {
			t.Fatalf("AddVariant() error = %v", err)
		}
	}

	return product
}

// price builds a THB price the test expects to be valid.
func price(t *testing.T, amountMinor int64) domain.Money {
	t.Helper()

	money, err := domain.NewMoney(amountMinor, "THB")
	if err != nil {
		t.Fatalf("NewMoney() error = %v", err)
	}

	return money
}
