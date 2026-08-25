// Package postgres is the driven adapter for storage: it maps between the
// domain aggregate and the rows sqlc generated, and turns pgx failures into
// classified errors.
//
// It is the only package in the service that knows a product is two tables.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/adapter/out/postgres/sqlc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/port/out"
)

// uniqueViolation is PostgreSQL's SQLSTATE for a duplicate key.
const uniqueViolation = "23505"

// skuConstraint is the constraint UNIQUE on variants.sku produces. It is matched
// by name rather than by code alone so that a primary key collision — two random
// UUIDs coming out the same, i.e. a bug — is reported as Internal rather than as
// something the caller did.
const skuConstraint = "variants_sku_key"

// emptyAttributes is what an attribute-less variant is stored as. The column is
// NOT NULL, and encoding/json renders a nil map as "null" rather than as the
// empty object the DEFAULT would have supplied.
var emptyAttributes = []byte(`{}`)

// ProductRepository implements the driven port over pgx.
type ProductRepository struct {
	pool *pgxpool.Pool
}

var _ out.ProductRepository = (*ProductRepository)(nil)

// NewProductRepository builds the repository over a pool.
func NewProductRepository(pool *pgxpool.Pool) *ProductRepository {
	return &ProductRepository{pool: pool}
}

// queries binds sqlc to whichever handle is correct for this call: the
// transaction txmanager put on the context, or the pool when there is none. It
// is what lets the same method run inside a use case's tx.Do and outside one
// without either saying so.
func (r *ProductRepository) queries(ctx context.Context) *sqlc.Queries {
	return sqlc.New(txmanager.From(ctx, r.pool))
}

// Create inserts the product and every variant it was born with.
//
// The caller is expected to have opened a transaction, because these are
// several statements describing one aggregate: a product row without its
// variants is not a state this service has a name for.
func (r *ProductRepository) Create(ctx context.Context, product *domain.Product) (*domain.Product, error) {
	snapshot := product.Snapshot()

	id, err := parseID(snapshot.ID)
	if err != nil {
		return nil, err
	}

	queries := r.queries(ctx)

	row, err := queries.CreateProduct(ctx, sqlc.CreateProductParams{
		ID:          id,
		Name:        snapshot.Name,
		Description: snapshot.Description,
		Category:    snapshot.Category.String(),
		Status:      snapshot.Status.String(),
	})
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "create product")
	}

	variants := make([]sqlc.Variant, 0, len(snapshot.Variants))

	for i := range snapshot.Variants {
		stored, err := insertVariant(ctx, queries, &snapshot.Variants[i])
		if err != nil {
			return nil, err
		}

		variants = append(variants, stored)
	}

	return toDomain(&row, variants), nil
}

// Update writes a loaded aggregate back: the product row under its optimistic
// lock, the variants added since it was read, and the ones that changed.
//
// Every variant is written rather than only those that differ. Telling them
// apart would mean holding the state the aggregate was loaded in, and a write
// that costs one statement per variant of one product is not what this schema is
// short of.
func (r *ProductRepository) Update(ctx context.Context, product *domain.Product) (*domain.Product, error) {
	snapshot := product.Snapshot()

	id, err := parseID(snapshot.ID)
	if err != nil {
		return nil, err
	}

	queries := r.queries(ctx)

	row, err := queries.UpdateProduct(ctx, sqlc.UpdateProductParams{
		ID:          id,
		Name:        snapshot.Name,
		Description: snapshot.Description,
		Category:    snapshot.Category.String(),
		Status:      snapshot.Status.String(),
		Version:     narrow(snapshot.Version),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The version moved between the read and this write. Not
			// KindNotFound: the caller loaded this aggregate moments ago in the
			// same transaction, so the row existing is not in doubt — and a
			// client told "not found" would stop retrying, where the answer
			// here is to re-read and try again.
			return nil, errorx.New(errorx.KindConflict, "product %s was modified concurrently", snapshot.ID).
				WithReason("PRODUCT_MODIFIED")
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "update product %s", snapshot.ID)
	}

	variants := make([]sqlc.Variant, 0, len(snapshot.Variants))

	for i := range snapshot.Variants {
		stored, err := writeVariant(ctx, queries, &snapshot.Variants[i])
		if err != nil {
			return nil, err
		}

		variants = append(variants, stored)
	}

	return toDomain(&row, variants), nil
}

// FindByID returns the product with its variants.
func (r *ProductRepository) FindByID(ctx context.Context, id domain.ProductID) (*domain.Product, error) {
	productID, err := parseID(id)
	if err != nil {
		return nil, err
	}

	row, err := r.queries(ctx).GetProductByID(ctx, productID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Not wrapped: only an Internal message is scrubbed by ToGRPC, so
			// this one reaches the client intact — and "no rows in result set"
			// is pgx's vocabulary rather than this service's.
			return nil, errorx.New(errorx.KindNotFound, "product %s not found", id).
				WithReason("PRODUCT_NOT_FOUND")
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "get product %s", id)
	}

	products, err := r.withVariants(ctx, []sqlc.Product{row})
	if err != nil {
		return nil, err
	}

	return products[0], nil
}

// FindByIDs returns the products that exist, and no error for the ones that do
// not.
func (r *ProductRepository) FindByIDs(ctx context.Context, ids []domain.ProductID) ([]*domain.Product, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	productIDs := make([]uuid.UUID, 0, len(ids))

	for _, id := range ids {
		productID, err := parseID(id)
		if err != nil {
			return nil, err
		}

		productIDs = append(productIDs, productID)
	}

	rows, err := r.queries(ctx).GetProductsByIDs(ctx, productIDs)
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "get %d product(s)", len(ids))
	}

	return r.withVariants(ctx, rows)
}

// FindVariantsBySKUs returns the variants a cart names, filtered to products in
// one state so that a draft is never priced for a buyer.
func (r *ProductRepository) FindVariantsBySKUs(
	ctx context.Context,
	skus []domain.SKU,
	status domain.ProductStatus,
) ([]*domain.Variant, error) {
	if len(skus) == 0 {
		return nil, nil
	}

	wanted := make([]string, 0, len(skus))
	for _, sku := range skus {
		wanted = append(wanted, sku.String())
	}

	rows, err := r.queries(ctx).GetVariantsBySKUs(ctx, sqlc.GetVariantsBySKUsParams{
		Skus:   wanted,
		Status: status.String(),
	})
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "get %d variant(s) by sku", len(skus))
	}

	variants := make([]*domain.Variant, 0, len(rows))
	for i := range rows {
		variants = append(variants, toDomainVariant(&rows[i]))
	}

	return variants, nil
}

// List returns one page of products, newest first.
//
// The two statements differ only in the category predicate, and picking between
// them here is what keeps each one on the index built for it.
func (r *ProductRepository) List(ctx context.Context, filter out.ProductFilter) ([]*domain.Product, error) {
	createdAt, id := cursor(filter.After)

	var (
		rows []sqlc.Product
		err  error
	)

	if filter.Category == "" {
		rows, err = r.queries(ctx).ListProducts(ctx, sqlc.ListProductsParams{
			Status:          filter.Status.String(),
			BeforeCreatedAt: createdAt,
			BeforeID:        id,
			RowLimit:        narrow(filter.Limit),
		})
	} else {
		rows, err = r.queries(ctx).ListProductsInCategory(ctx, sqlc.ListProductsInCategoryParams{
			Status:          filter.Status.String(),
			Category:        filter.Category.String(),
			BeforeCreatedAt: createdAt,
			BeforeID:        id,
			RowLimit:        narrow(filter.Limit),
		})
	}

	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "list products")
	}

	return r.withVariants(ctx, rows)
}

// withVariants loads the variants for a set of product rows and assembles the
// aggregates, keeping the order the products arrived in — which for a listing is
// the order the ORDER BY decided and the cursor depends on.
func (r *ProductRepository) withVariants(ctx context.Context, rows []sqlc.Product) ([]*domain.Product, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	ids := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}

	// One query for the whole page rather than one per product: this read backs
	// a listing, and a query per row is how a page of twenty becomes twenty-one
	// round trips.
	variants, err := r.queries(ctx).GetVariantsByProductIDs(ctx, ids)
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "get variants for %d product(s)", len(rows))
	}

	byProduct := make(map[uuid.UUID][]sqlc.Variant, len(rows))
	for i := range variants {
		byProduct[variants[i].ProductID] = append(byProduct[variants[i].ProductID], variants[i])
	}

	products := make([]*domain.Product, 0, len(rows))
	for i := range rows {
		products = append(products, toDomain(&rows[i], byProduct[rows[i].ID]))
	}

	return products, nil
}

// writeVariant inserts a variant that has never been stored and updates one that
// has. The zero created_at is the signal, because it is the database that fills
// that column and the aggregate has no other way to say which of its variants
// are new.
func writeVariant(ctx context.Context, queries *sqlc.Queries, variant *domain.VariantSnapshot) (sqlc.Variant, error) {
	if variant.CreatedAt.IsZero() {
		return insertVariant(ctx, queries, variant)
	}

	id, err := parseVariantID(variant.ID)
	if err != nil {
		return sqlc.Variant{}, err
	}

	attributes, err := marshalAttributes(variant.Attributes)
	if err != nil {
		return sqlc.Variant{}, err
	}

	row, err := queries.UpdateVariant(ctx, sqlc.UpdateVariantParams{
		ID:               id,
		PriceAmountMinor: variant.Price.AmountMinor(),
		PriceCurrency:    variant.Price.Currency().String(),
		Attributes:       attributes,
		Version:          narrow(variant.Version),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.Variant{}, errorx.New(errorx.KindConflict, "variant %s was modified concurrently", variant.ID).
				WithReason("VARIANT_MODIFIED")
		}

		return sqlc.Variant{}, errorx.Wrap(err, errorx.KindInternal, "update variant %s", variant.ID)
	}

	return row, nil
}

// insertVariant stores a variant that did not exist before.
func insertVariant(ctx context.Context, queries *sqlc.Queries, variant *domain.VariantSnapshot) (sqlc.Variant, error) {
	id, err := parseVariantID(variant.ID)
	if err != nil {
		return sqlc.Variant{}, err
	}

	productID, err := parseID(variant.ProductID)
	if err != nil {
		return sqlc.Variant{}, err
	}

	attributes, err := marshalAttributes(variant.Attributes)
	if err != nil {
		return sqlc.Variant{}, err
	}

	row, err := queries.CreateVariant(ctx, sqlc.CreateVariantParams{
		ID:               id,
		ProductID:        productID,
		Sku:              variant.SKU.String(),
		PriceAmountMinor: variant.Price.AmountMinor(),
		PriceCurrency:    variant.Price.Currency().String(),
		Attributes:       attributes,
	})
	if err != nil {
		if isConstraintViolation(err, skuConstraint) {
			// New rather than Wrap, because only an Internal message is
			// scrubbed by ToGRPC: wrapping would describe the schema to anyone
			// who can reach the API. The SKU is in the metadata because the
			// caller chose it and a client fixing this needs to know which one
			// of a batch collided.
			return sqlc.Variant{}, errorx.New(errorx.KindConflict, "sku is already in use").
				WithReason("SKU_ALREADY_EXISTS").
				WithMetadata(map[string]string{"sku": variant.SKU.String()})
		}

		return sqlc.Variant{}, errorx.Wrap(err, errorx.KindInternal, "create variant %s", variant.SKU)
	}

	return row, nil
}

// toDomain rebuilds the aggregate from its rows. It reconstitutes rather than
// constructs: re-running today's validation over yesterday's data is how a
// service loses the ability to read the products it created itself.
// toDomainVariant rebuilds one variant on its own, for the read that answers
// about SKUs rather than about products.
func toDomainVariant(row *sqlc.Variant) *domain.Variant {
	return domain.ReconstituteVariant(domain.VariantSnapshot{
		ID:         domain.VariantID(row.ID.String()),
		ProductID:  domain.ProductID(row.ProductID.String()),
		SKU:        domain.SKU(row.Sku),
		Price:      domain.ReconstituteMoney(row.PriceAmountMinor, domain.CurrencyCode(row.PriceCurrency)),
		Attributes: unmarshalAttributes(row.Attributes),
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
		Version:    int(row.Version),
	})
}

func toDomain(row *sqlc.Product, variants []sqlc.Variant) *domain.Product {
	snapshots := make([]domain.VariantSnapshot, 0, len(variants))

	for i := range variants {
		snapshots = append(snapshots, domain.VariantSnapshot{
			ID:         domain.VariantID(variants[i].ID.String()),
			ProductID:  domain.ProductID(variants[i].ProductID.String()),
			SKU:        domain.SKU(variants[i].Sku),
			Price:      domain.ReconstituteMoney(variants[i].PriceAmountMinor, domain.CurrencyCode(variants[i].PriceCurrency)),
			Attributes: unmarshalAttributes(variants[i].Attributes),
			CreatedAt:  variants[i].CreatedAt,
			UpdatedAt:  variants[i].UpdatedAt,
			Version:    int(variants[i].Version),
		})
	}

	return domain.ReconstituteProduct(domain.ProductSnapshot{
		ID:          domain.ProductID(row.ID.String()),
		Name:        row.Name,
		Description: row.Description,
		Category:    domain.Category(row.Category),
		Status:      domain.ProductStatus(row.Status),
		Variants:    snapshots,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		Version:     int(row.Version),
	})
}

// marshalAttributes renders the map for the jsonb column.
func marshalAttributes(attributes map[string]string) ([]byte, error) {
	if len(attributes) == 0 {
		return emptyAttributes, nil
	}

	encoded, err := json.Marshal(attributes)
	if err != nil {
		// Unreachable for a map of strings, and Internal because reaching it
		// would mean the standard library refused to encode one.
		return nil, errorx.Wrap(err, errorx.KindInternal, "encode variant attributes")
	}

	return encoded, nil
}

// unmarshalAttributes reads the column back, and returns nil for an empty
// object so that a variant with no attributes is the same value whether it came
// from the aggregate or from a row.
//
// A column this service cannot parse yields no attributes rather than an error:
// it is descriptive text with no rule attached, and failing the read would take
// a whole storefront down over one malformed row.
func unmarshalAttributes(encoded []byte) map[string]string {
	if len(encoded) == 0 {
		return nil
	}

	attributes := map[string]string{}
	if err := json.Unmarshal(encoded, &attributes); err != nil {
		return nil
	}

	if len(attributes) == 0 {
		return nil
	}

	return attributes
}

// cursor turns a page boundary into the two nullable parameters the listing
// statements take. Nil means the first page, which the SQL reads as the largest
// value either column can hold.
func cursor(after *out.ProductCursor) (pgtype.Timestamptz, uuid.NullUUID) {
	if after == nil {
		return pgtype.Timestamptz{}, uuid.NullUUID{}
	}

	id, err := uuid.Parse(after.ID.String())
	if err != nil {
		// A cursor that does not parse is treated as no cursor: the id came
		// back through a page token the use case already validated, and failing
		// the listing over it would answer an error where the first page is
		// both harmless and what a client with a stale token wants.
		return pgtype.Timestamptz{}, uuid.NullUUID{}
	}

	return pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}, uuid.NullUUID{UUID: id, Valid: true}
}

// parseID converts an aggregate identifier for pgx.
func parseID(id domain.ProductID) (uuid.UUID, error) {
	parsed, err := uuid.Parse(id.String())
	if err != nil {
		// Unreachable through the domain constructors and the parse the use
		// case runs first, which is exactly why it is Internal: reaching it
		// means an aggregate was built with an id this package cannot store.
		return uuid.UUID{}, errorx.Wrap(err, errorx.KindInternal, "parse product id")
	}

	return parsed, nil
}

// parseVariantID converts a variant identifier for pgx.
func parseVariantID(id domain.VariantID) (uuid.UUID, error) {
	parsed, err := uuid.Parse(id.String())
	if err != nil {
		return uuid.UUID{}, errorx.Wrap(err, errorx.KindInternal, "parse variant id")
	}

	return parsed, nil
}

// narrow converts a count the domain keeps as an int to the int32 the columns
// are. The values that reach it — a version and a page size — are bounded far
// below this by the schema and the use case; the clamp is here so that the
// conversion cannot silently wrap if one day they are not.
func narrow(value int) int32 {
	switch {
	case value < 0:
		return 0
	case value > math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(value)
	}
}

// isConstraintViolation reports whether err is a unique violation of the named
// constraint.
func isConstraintViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == uniqueViolation && pgErr.ConstraintName == constraint
}
