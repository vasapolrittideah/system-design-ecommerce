// Package out declares the driven ports: what the catalog service needs from
// the world outside it, in the vocabulary of the use cases that call them rather
// than of the adapters that implement them.
package out

import (
	"context"
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/domain"
)

// ProductCursor is where a page of a listing stopped.
//
// Both halves are needed: created_at alone cannot separate two products written
// in the same transaction, and paging on a non-unique key either skips or
// repeats whatever shares the boundary value.
type ProductCursor struct {
	CreatedAt time.Time
	ID        domain.ProductID
}

// ProductFilter is one page of a browse, already normalised.
type ProductFilter struct {
	// Status is exactly one state. A listing that could return any state would
	// be a way for the storefront to show an unfinished draft.
	Status domain.ProductStatus

	// Category is an exact slug, or empty for every category.
	Category domain.Category

	// After is nil for the first page.
	After *ProductCursor

	// Limit is how many rows to return, and the caller asks for one more than
	// it means to show — a full page is how it learns another exists, without a
	// second query counting rows nobody will read.
	Limit int
}

// ProductRepository stores and reads product aggregates.
//
// Every method takes a context because the adapter pulls the current
// transaction off it — a use case that wraps a read and a write in txmanager.Do
// gets them in one transaction without any of these signatures changing.
//
// The aggregate is always whole: a product arrives with its variants and is
// written with them, because the rules protecting it — one currency, no
// repeated SKU — cannot be checked against variants the caller never loaded.
type ProductRepository interface {
	// Create persists a product that has never been stored, with whatever
	// variants it was born with, and returns it as the database recorded it —
	// which is where created_at and updated_at come from.
	//
	// A SKU already used by another product comes back as a conflict, so the
	// caller does not check first: the UNIQUE constraint is the guard, and
	// checking then inserting leaves a window where two requests both find
	// nothing and both proceed.
	Create(ctx context.Context, product *domain.Product) (*domain.Product, error)

	// Update writes a loaded aggregate back: the product row, the variants that
	// changed, and the ones added since it was read.
	//
	// It fails with a conflict when the version it carries is no longer the
	// stored one, which is the whole of the optimistic lock — the loser of two
	// concurrent writers affects no rows and is told so, rather than silently
	// overwriting what it never read.
	Update(ctx context.Context, product *domain.Product) (*domain.Product, error)

	// FindByID returns the product with its variants, or a not-found error when
	// there is none.
	FindByID(ctx context.Context, id domain.ProductID) (*domain.Product, error)

	// FindByIDs returns the products that exist, in no guaranteed order and
	// possibly fewer than were asked for. A missing id is not an error.
	FindByIDs(ctx context.Context, ids []domain.ProductID) ([]*domain.Product, error)

	// List returns a page of products, newest first, ordered so that the cursor
	// in ProductFilter means the same thing on the next call.
	List(ctx context.Context, filter ProductFilter) ([]*domain.Product, error)
}
