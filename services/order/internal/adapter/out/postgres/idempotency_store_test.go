package postgres_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
)

const key = "checkout-0001"

var hash = []byte("the cart this key was first used for")

func TestClaimTakesAFreeKey(t *testing.T) {
	ctx := context.Background()
	_, orders, idem := setup(t)

	created, err := orders.Create(ctx, newOrder(t, userID, reservationID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	claim, taken, err := idem.Claim(ctx, userID, key, hash, created.ID())
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}
	if !taken {
		t.Fatal("Claim() reported the key was taken, want it free")
	}
	if claim.OrderID != created.ID() {
		t.Errorf("claim order = %q, want %q", claim.OrderID, created.ID())
	}
}

func TestClaimReportsTheAnswerAKeyAlreadyHas(t *testing.T) {
	ctx := context.Background()
	_, orders, idem := setup(t)

	first, err := orders.Create(ctx, newOrder(t, userID, reservationID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	if _, _, err := idem.Claim(ctx, userID, key, hash, first.ID()); err != nil {
		t.Fatalf("first Claim() error = %v, want nil", err)
	}

	second, err := orders.Create(ctx, newOrder(t, userID, domain.ReservationID(domain.NewOrderID())))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	// A retry that got as far as the transaction. The key is not free, and what
	// comes back is what the first attempt stored — the answer to replay.
	claim, taken, err := idem.Claim(ctx, userID, key, hash, second.ID())
	if err != nil {
		t.Fatalf("second Claim() error = %v, want nil", err)
	}
	if taken {
		t.Fatal("Claim() took a key that was already held")
	}
	if claim.OrderID != first.ID() {
		t.Errorf("claim order = %q, want the first attempt's %q", claim.OrderID, first.ID())
	}
	// The stored hash is the first request's, never overwritten by the second:
	// it is what tells a retry from a key reused for a different cart.
	if !bytes.Equal(claim.RequestHash, hash) {
		t.Errorf("claim hash = %q, want the one the key was first used with", claim.RequestHash)
	}
}

func TestClaimIsScopedToTheUser(t *testing.T) {
	ctx := context.Background()
	_, orders, idem := setup(t)

	mine, err := orders.Create(ctx, newOrder(t, userID, reservationID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	theirs, err := orders.Create(ctx, newOrder(t, otherUserID, domain.ReservationID(domain.NewOrderID())))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if _, taken, err := idem.Claim(ctx, userID, key, hash, mine.ID()); err != nil || !taken {
		t.Fatalf("Claim() for the first user = %v, %v", taken, err)
	}

	// Two clients that both send "checkout-1" are two claims, not one
	// collision.
	if _, taken, err := idem.Claim(ctx, otherUserID, key, hash, theirs.ID()); err != nil || !taken {
		t.Fatalf("Claim() for the second user = %v, %v", taken, err)
	}
}

func TestFindReportsAFreeKey(t *testing.T) {
	ctx := context.Background()
	_, _, idem := setup(t)

	_, found, err := idem.Find(ctx, userID, key)
	if err != nil {
		t.Fatalf("Find() error = %v, want nil", err)
	}
	if found {
		t.Error("Find() reported a claim on a key nobody has used")
	}
}

func TestFindReadsBackWhatWasClaimed(t *testing.T) {
	ctx := context.Background()
	_, orders, idem := setup(t)

	created, err := orders.Create(ctx, newOrder(t, userID, reservationID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	if _, _, err := idem.Claim(ctx, userID, key, hash, created.ID()); err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}

	claim, found, err := idem.Find(ctx, userID, key)
	if err != nil {
		t.Fatalf("Find() error = %v, want nil", err)
	}
	if !found {
		t.Fatal("Find() reported no claim, want the one just taken")
	}
	if claim.OrderID != created.ID() || !bytes.Equal(claim.RequestHash, hash) {
		t.Errorf("claim = %+v, want the order and hash that were stored", claim)
	}
}
