package domain

import "time"

// StockItem is how many of one SKU the warehouse has, split between what is
// still sellable and what a live reservation is holding.
//
// It has no mutating methods, and that is the decision the package comment
// describes rather than an aggregate somebody forgot to finish. Every change to
// these counts is a conditional UPDATE whose predicate is evaluated by the
// server against the current row; a Reserve method here would have to read the
// numbers first, and a number read first is the one thing this service may not
// base a decision on.
//
// What it is instead: the shape those counts have everywhere outside the
// repository, and the thing the RPCs answer with.
type StockItem struct {
	id        StockItemID
	sku       SKU
	available Quantity
	reserved  Quantity

	// Set by the database on insert and read back, never chosen here: two
	// replicas disagree about the current time by more than the ordering of two
	// stock items is worth.
	createdAt time.Time
	updatedAt time.Time

	// version is carried for consistency with the rest of the schema and is not
	// the guard on this row. See the migration.
	version int
}

// NewStockItem starts tracking a SKU at a starting count.
//
// The identifier is minted here rather than by the database, because the domain
// events an aggregate raises carry that id and are written to the outbox in the
// same transaction as the row.
func NewStockItem(sku SKU, available Quantity) *StockItem {
	return &StockItem{
		id:        NewStockItemID(),
		sku:       sku,
		available: available,
		version:   1,
	}
}

// ID returns the row's identifier.
func (i *StockItem) ID() StockItemID { return i.id }

// SKU returns the key anything actually looks this count up by.
func (i *StockItem) SKU() SKU { return i.sku }

// Available returns what a new reservation may still take.
func (i *StockItem) Available() Quantity { return i.available }

// Reserved returns what live reservations are holding.
func (i *StockItem) Reserved() Quantity { return i.reserved }

// CreatedAt is the zero time until the row has been written.
func (i *StockItem) CreatedAt() time.Time { return i.createdAt }

// UpdatedAt is the zero time until the row has been written.
func (i *StockItem) UpdatedAt() time.Time { return i.updatedAt }

// Version is what the row currently carries.
func (i *StockItem) Version() int { return i.version }

// StockItemSnapshot is the whole state of a stock item as it is stored.
type StockItemSnapshot struct {
	ID        StockItemID
	SKU       SKU
	Available Quantity
	Reserved  Quantity
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int
}

// ReconstituteStockItem rebuilds a stock item from storage.
//
// It validates nothing, and that is deliberate: a bound tightened afterwards must
// not make existing rows unreadable. New values come in through NewStockItem and
// the statements that move the counts, which do check.
func ReconstituteStockItem(s StockItemSnapshot) *StockItem {
	return &StockItem{
		id:        s.ID,
		sku:       s.SKU,
		available: s.Available,
		reserved:  s.Reserved,
		createdAt: s.CreatedAt,
		updatedAt: s.UpdatedAt,
		version:   s.Version,
	}
}

// Snapshot returns the stock item's state for a repository to persist.
func (i *StockItem) Snapshot() StockItemSnapshot {
	return StockItemSnapshot{
		ID:        i.id,
		SKU:       i.sku,
		Available: i.available,
		Reserved:  i.reserved,
		CreatedAt: i.createdAt,
		UpdatedAt: i.updatedAt,
		Version:   i.version,
	}
}
