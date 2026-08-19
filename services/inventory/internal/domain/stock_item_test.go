package domain_test

import (
	"testing"
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
)

func TestNewStockItem(t *testing.T) {
	item := domain.NewStockItem("SHIRT-M", 5)

	if got := item.Available(); got != 5 {
		t.Errorf("Available() = %d, want 5", got)
	}

	// Nothing is held until something reserves it, and the id is minted here
	// rather than by the database because the events this aggregate raises carry
	// it into the outbox in the same transaction as the row.
	if got := item.Reserved(); got != 0 {
		t.Errorf("Reserved() = %d, want 0", got)
	}

	if item.ID() == "" {
		t.Error("ID() is empty, want a minted identifier")
	}

	if got := item.Version(); got != 1 {
		t.Errorf("Version() = %d, want 1", got)
	}

	// The database fills these on insert, so an aggregate that has never been
	// written has neither — which is what a repository reads to tell a new row
	// from one it is updating.
	if !item.CreatedAt().IsZero() || !item.UpdatedAt().IsZero() {
		t.Errorf("timestamps = %v/%v, want the zero time before the row exists",
			item.CreatedAt(), item.UpdatedAt())
	}
}

func TestStockItemRoundTrip(t *testing.T) {
	stored := domain.StockItemSnapshot{
		ID:        domain.NewStockItemID(),
		SKU:       "SHIRT-M",
		Available: 12,
		Reserved:  3,
		CreatedAt: time.Date(2026, time.August, 1, 9, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, time.August, 19, 9, 0, 0, 0, time.UTC),
		Version:   7,
	}

	if got := domain.ReconstituteStockItem(stored).Snapshot(); got != stored {
		t.Errorf("Snapshot() = %+v, want the snapshot it was built from %+v", got, stored)
	}
}

func TestReconstituteStockItemValidatesNothing(t *testing.T) {
	// A count written before the ceiling existed. Refusing to load it would make
	// the SKU unreadable and its held stock unreturnable, which is a worse
	// outcome than a number nobody would write today.
	item := domain.ReconstituteStockItem(domain.StockItemSnapshot{
		ID:        domain.NewStockItemID(),
		SKU:       "SHIRT-M",
		Available: 5_000_000,
		Version:   2,
	})

	if got := item.Available(); got != 5_000_000 {
		t.Errorf("Available() = %d, want the stored 5000000", got)
	}
}
