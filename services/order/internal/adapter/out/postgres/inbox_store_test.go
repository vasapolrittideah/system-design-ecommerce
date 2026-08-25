package postgres_test

import (
	"context"
	"testing"

	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/adapter/out/postgres"
)

const eventID = "9c1a2b3d-4e5f-4061-8a2b-3c4d5e6f7a8b"

func TestClaimTakesAFreeEvent(t *testing.T) {
	ctx := context.Background()
	pool, _, _ := setup(t)
	inbox := adapter.NewInboxStore(pool)

	claimed, err := inbox.Claim(ctx, "order.checkout-saga", eventID)
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}
	if !claimed {
		t.Fatal("Claim() reported the event already claimed, want it free")
	}
}

// The whole point of the inbox: a redelivery must find the event already
// claimed rather than let the caller do the work a second time.
func TestClaimRefusesARedelivery(t *testing.T) {
	ctx := context.Background()
	pool, _, _ := setup(t)
	inbox := adapter.NewInboxStore(pool)

	if _, err := inbox.Claim(ctx, "order.checkout-saga", eventID); err != nil {
		t.Fatalf("first Claim() error = %v, want nil", err)
	}

	claimed, err := inbox.Claim(ctx, "order.checkout-saga", eventID)
	if err != nil {
		t.Fatalf("second Claim() error = %v, want nil", err)
	}
	if claimed {
		t.Fatal("second Claim() reported the event free, want it already claimed")
	}
}

// Two consumer groups reading the same event are two separate claims: each
// group wants its own copy of the work done.
func TestClaimIsScopedByConsumerGroup(t *testing.T) {
	ctx := context.Background()
	pool, _, _ := setup(t)
	inbox := adapter.NewInboxStore(pool)

	if _, err := inbox.Claim(ctx, "group-a", eventID); err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}

	claimed, err := inbox.Claim(ctx, "group-b", eventID)
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}
	if !claimed {
		t.Fatal("Claim() reported the event already claimed for a different group, want it free")
	}
}
