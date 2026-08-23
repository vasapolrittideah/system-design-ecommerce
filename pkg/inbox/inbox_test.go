package inbox_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/inbox"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres/postgrestest"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
)

const (
	group   = "order-worker"
	eventID = "01234567-89ab-cdef-0123-456789abcdef"
)

// shipments stands in for whatever a consumer writes when it handles an event.
// The guarantee under test is that the claim and that write move together.
const shipments = `CREATE TABLE shipments (id text PRIMARY KEY)`

func setup(t *testing.T) *pgxpool.Pool {
	t.Helper()

	// The schema under test is the one services are told to copy, so the tests
	// run against that file rather than a convenient paraphrase of it.
	schema, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("read schema.sql: %v", err)
	}

	return postgrestest.New(t, string(schema), shipments)
}

func claims(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM processed_events`).Scan(&n); err != nil {
		t.Fatalf("count processed_events: %v", err)
	}

	return n
}

func TestClaimTakesTheFirstDelivery(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)

	claimed, err := inbox.Claim(ctx, pool, group, eventID)
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}
	if !claimed {
		t.Error("Claim() = false, want true for an event nobody has handled")
	}

	var gotGroup, gotEvent string
	var processedAt time.Time
	sql := `SELECT consumer_group, event_id, processed_at FROM processed_events`
	if err := pool.QueryRow(ctx, sql).Scan(&gotGroup, &gotEvent, &processedAt); err != nil {
		t.Fatalf("read the claim: %v", err)
	}

	if gotGroup != group {
		t.Errorf("consumer_group = %q, want %q", gotGroup, group)
	}
	if gotEvent != eventID {
		t.Errorf("event_id = %q, want %q", gotEvent, eventID)
	}
	if processedAt.IsZero() {
		t.Error("processed_at is zero, want the insert time")
	}
}

func TestClaimReportsARedeliveryAsAlreadyHandled(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)

	if _, err := inbox.Claim(ctx, pool, group, eventID); err != nil {
		t.Fatalf("first Claim() error = %v, want nil", err)
	}

	// The same event arriving again is the at-least-once case, not a failure.
	claimed, err := inbox.Claim(ctx, pool, group, eventID)
	if err != nil {
		t.Fatalf("second Claim() error = %v, want nil", err)
	}
	if claimed {
		t.Error("Claim() = true, want false for an event already handled")
	}

	if n := claims(t, ctx, pool); n != 1 {
		t.Errorf("processed_events has %d rows, want 1", n)
	}
}

func TestClaimIsPerConsumerGroup(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)

	// Two groups both want OrderPaid; neither may hide it from the other.
	for _, g := range []string{"shipping-worker", "notification-worker"} {
		claimed, err := inbox.Claim(ctx, pool, g, eventID)
		if err != nil {
			t.Fatalf("Claim() for %s error = %v, want nil", g, err)
		}
		if !claimed {
			t.Errorf("Claim() for %s = false, want true", g)
		}
	}

	if n := claims(t, ctx, pool); n != 2 {
		t.Errorf("processed_events has %d rows, want 2", n)
	}
}

func TestClaimRollsBackWithTheWork(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	wantErr := errors.New("shipping label provider rejected the address")

	err := txmanager.New(pool).Do(ctx, func(ctx context.Context) error {
		db := txmanager.From(ctx, pool)

		claimed, err := inbox.Claim(ctx, db, group, eventID)
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}
		if _, err := db.Exec(ctx, `INSERT INTO shipments (id) VALUES ($1)`, "shipment-1"); err != nil {
			return err
		}

		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Do() error = %v, want %v", err, wantErr)
	}

	// The point of the pattern: work that did not happen was not recorded as
	// having happened, so the redelivery this consumer is about to get is the
	// one that does it.
	if n := claims(t, ctx, pool); n != 0 {
		t.Errorf("processed_events has %d rows, want 0 after the transaction rolled back", n)
	}

	claimed, err := inbox.Claim(ctx, pool, group, eventID)
	if err != nil {
		t.Fatalf("Claim() after rollback error = %v, want nil", err)
	}
	if !claimed {
		t.Error("Claim() after rollback = false, want true")
	}
}

func TestClaimCommitsWithTheWork(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)

	err := txmanager.New(pool).Do(ctx, func(ctx context.Context) error {
		db := txmanager.From(ctx, pool)

		if _, err := inbox.Claim(ctx, db, group, eventID); err != nil {
			return err
		}
		_, err := db.Exec(ctx, `INSERT INTO shipments (id) VALUES ($1)`, "shipment-1")

		return err
	})
	if err != nil {
		t.Fatalf("Do() error = %v, want nil", err)
	}

	if n := claims(t, ctx, pool); n != 1 {
		t.Errorf("processed_events has %d rows, want 1", n)
	}
}

func TestClaimResolvesConcurrentDeliveriesOnThePrimaryKey(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)

	first, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin first transaction: %v", err)
	}
	defer first.Rollback(ctx) //nolint:errcheck // the test commits it below

	claimed, err := inbox.Claim(ctx, first, group, eventID)
	if err != nil {
		t.Fatalf("first Claim() error = %v, want nil", err)
	}
	if !claimed {
		t.Fatal("first Claim() = false, want true")
	}

	// The second delivery blocks on the primary key until the first
	// transaction ends, which is what makes two consumers reading the same
	// partition at once safe without either of them locking anything itself.
	type result struct {
		claimed bool
		err     error
	}
	done := make(chan result, 1)

	go func() {
		second, err := pool.Begin(ctx)
		if err != nil {
			done <- result{err: err}

			return
		}
		defer second.Rollback(ctx) //nolint:errcheck // nothing was written to keep

		claimed, err := inbox.Claim(ctx, second, group, eventID)
		done <- result{claimed: claimed, err: err}
	}()

	// Long enough for the second claim to reach the index and block on it. Too
	// short only costs the test its concurrency, never its verdict.
	time.Sleep(100 * time.Millisecond)

	if err := first.Commit(ctx); err != nil {
		t.Fatalf("commit first transaction: %v", err)
	}

	got := <-done
	if got.err != nil {
		t.Fatalf("second Claim() error = %v, want nil", got.err)
	}
	if got.claimed {
		t.Error("second Claim() = true, want false once the first one committed")
	}
}

func TestClaimRejectsIncompleteClaims(t *testing.T) {
	tests := []struct {
		name          string
		consumerGroup string
		eventID       string
	}{
		{"no consumer group", "", eventID},
		{"no event id", group, ""},
		{"neither", "", ""},
	}

	ctx := context.Background()
	pool := setup(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claimed, err := inbox.Claim(ctx, pool, tt.consumerGroup, tt.eventID)
			if !errors.Is(err, inbox.ErrIncompleteClaim) {
				t.Fatalf("Claim() error = %v, want ErrIncompleteClaim", err)
			}
			if claimed {
				t.Error("Claim() = true, want false alongside an error")
			}
		})
	}

	// An empty field claimed rather than refused is a row every later event
	// collides with, and a topic silently skipped from there on.
	if n := claims(t, ctx, pool); n != 0 {
		t.Errorf("processed_events has %d rows, want 0", n)
	}
}
