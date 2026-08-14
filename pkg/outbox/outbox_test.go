// These tests live in the package rather than beside it so the relay tests can
// drive one publish cycle at a time instead of racing a running loop. The
// helpers here are shared with relay_test.go.
package outbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres/postgrestest"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
)

// orders stands in for whatever aggregate a service persists alongside its
// events. The guarantee under test is that the two move together.
const orders = `CREATE TABLE orders (id text PRIMARY KEY)`

type row struct {
	ID            int64
	AggregateType string
	AggregateID   string
	EventType     string
	Topic         string
	Payload       []byte
	Headers       map[string]string
	CreatedAt     time.Time
	PublishedAt   *time.Time
}

func setup(t *testing.T) *pgxpool.Pool {
	t.Helper()

	// The schema under test is the one services are told to copy, so the tests
	// run against that file rather than a convenient paraphrase of it.
	schema, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("read schema.sql: %v", err)
	}

	return postgrestest.New(t, string(schema), orders)
}

func record() Record {
	return Record{
		AggregateType: "order",
		AggregateID:   "01234567-89ab-cdef-0123-456789abcdef",
		EventType:     "OrderPaid",
		Topic:         "ecommerce.order.events.v1",
		Payload:       []byte{0x0a, 0x02, 0x68, 0x69},
		Headers:       map[string]string{"traceparent": "00-trace-span-01"},
	}
}

func readAll(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []row {
	t.Helper()

	sql := `SELECT id, aggregate_type, aggregate_id, event_type, topic, payload, headers, created_at, published_at
	        FROM outbox ORDER BY id`

	rows, err := pool.Query(ctx, sql)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()

	var out []row
	for rows.Next() {
		var r row
		err := rows.Scan(&r.ID, &r.AggregateType, &r.AggregateID, &r.EventType,
			&r.Topic, &r.Payload, &r.Headers, &r.CreatedAt, &r.PublishedAt)
		if err != nil {
			t.Fatalf("scan outbox row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read outbox rows: %v", err)
	}

	return out
}

func TestWritePersistsEveryField(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	want := record()

	if err := Write(ctx, pool, want); err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}

	rows := readAll(t, ctx, pool)
	if len(rows) != 1 {
		t.Fatalf("outbox has %d rows, want 1", len(rows))
	}

	got := rows[0]
	if got.AggregateType != want.AggregateType {
		t.Errorf("aggregate_type = %q, want %q", got.AggregateType, want.AggregateType)
	}
	if got.AggregateID != want.AggregateID {
		t.Errorf("aggregate_id = %q, want %q", got.AggregateID, want.AggregateID)
	}
	if got.EventType != want.EventType {
		t.Errorf("event_type = %q, want %q", got.EventType, want.EventType)
	}
	if got.Topic != want.Topic {
		t.Errorf("topic = %q, want %q", got.Topic, want.Topic)
	}
	if !bytes.Equal(got.Payload, want.Payload) {
		t.Errorf("payload = %v, want %v", got.Payload, want.Payload)
	}
	if got.Headers["traceparent"] != want.Headers["traceparent"] {
		t.Errorf("headers = %v, want %v", got.Headers, want.Headers)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at is zero, want the insert time")
	}
	// A row is a queue entry until the relay has published it.
	if got.PublishedAt != nil {
		t.Errorf("published_at = %v, want NULL", got.PublishedAt)
	}
}

func TestWriteKeepsTheOrderEventsWereRaisedIn(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)

	first, second, third := record(), record(), record()
	first.EventType, second.EventType, third.EventType = "OrderCreated", "OrderPaid", "OrderShipped"

	if err := Write(ctx, pool, first, second, third); err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}

	rows := readAll(t, ctx, pool)
	if len(rows) != 3 {
		t.Fatalf("outbox has %d rows, want 3", len(rows))
	}

	// Consumers depend on OrderPaid never arriving before OrderCreated, and the
	// relay publishes by id, so the ids have to follow the argument order.
	want := []string{"OrderCreated", "OrderPaid", "OrderShipped"}
	for i, r := range rows {
		if r.EventType != want[i] {
			t.Errorf("row %d event_type = %q, want %q", i, r.EventType, want[i])
		}
		if i > 0 && r.ID <= rows[i-1].ID {
			t.Errorf("row %d id = %d, want greater than %d", i, r.ID, rows[i-1].ID)
		}
	}
}

func TestWriteRollsBackWithTheAggregate(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	manager := txmanager.New(pool)
	wantErr := errors.New("payment gateway rejected the order")

	err := manager.Do(ctx, func(ctx context.Context) error {
		db := txmanager.From(ctx, pool)

		if _, err := db.Exec(ctx, `INSERT INTO orders (id) VALUES ($1)`, "order-1"); err != nil {
			return err
		}
		if err := Write(ctx, db, record()); err != nil {
			return err
		}

		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Do() error = %v, want %v", err, wantErr)
	}

	// The point of the pattern: no event survives an aggregate that did not.
	if rows := readAll(t, ctx, pool); len(rows) != 0 {
		t.Errorf("outbox has %d rows, want 0 after the transaction rolled back", len(rows))
	}
}

func TestWriteCommitsWithTheAggregate(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)

	err := txmanager.New(pool).Do(ctx, func(ctx context.Context) error {
		db := txmanager.From(ctx, pool)

		if _, err := db.Exec(ctx, `INSERT INTO orders (id) VALUES ($1)`, "order-1"); err != nil {
			return err
		}

		return Write(ctx, db, record())
	})
	if err != nil {
		t.Fatalf("Do() error = %v, want nil", err)
	}

	if rows := readAll(t, ctx, pool); len(rows) != 1 {
		t.Errorf("outbox has %d rows, want 1", len(rows))
	}
}

func TestWriteDefaultsHeadersToAnEmptyObject(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)

	rec := record()
	rec.Headers = nil

	if err := Write(ctx, pool, rec); err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}

	rows := readAll(t, ctx, pool)
	if len(rows) != 1 {
		t.Fatalf("outbox has %d rows, want 1", len(rows))
	}
	// NULL and JSON null would both force the relay to special-case a row that
	// simply has no headers.
	if rows[0].Headers == nil {
		t.Error("headers scanned as nil, want an empty object")
	}
	if len(rows[0].Headers) != 0 {
		t.Errorf("headers = %v, want empty", rows[0].Headers)
	}
}

func TestWriteWithoutRecordsIsANoOp(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)

	if err := Write(ctx, pool); err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}

	if rows := readAll(t, ctx, pool); len(rows) != 0 {
		t.Errorf("outbox has %d rows, want 0", len(rows))
	}
}

func TestWriteRejectsIncompleteRecords(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{"no aggregate type", func(r *Record) { r.AggregateType = "" }},
		{"no aggregate id", func(r *Record) { r.AggregateID = "" }},
		{"no event type", func(r *Record) { r.EventType = "" }},
		{"no topic", func(r *Record) { r.Topic = "" }},
		{"no payload", func(r *Record) { r.Payload = nil }},
	}

	ctx := context.Background()
	pool := setup(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := record()
			tt.mutate(&rec)

			err := Write(ctx, pool, rec)
			if !errors.Is(err, ErrIncompleteRecord) {
				t.Fatalf("Write() error = %v, want ErrIncompleteRecord", err)
			}
		})
	}

	// A rejected batch must not have written anything, valid records included.
	if rows := readAll(t, ctx, pool); len(rows) != 0 {
		t.Errorf("outbox has %d rows, want 0", len(rows))
	}
}

func TestWriteRejectsABatchWholesale(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)

	valid := record()
	invalid := record()
	invalid.Topic = ""

	if err := Write(ctx, pool, valid, invalid); !errors.Is(err, ErrIncompleteRecord) {
		t.Fatalf("Write() error = %v, want ErrIncompleteRecord", err)
	}

	// Publishing half of what an aggregate raised would be worse than
	// publishing none of it.
	if rows := readAll(t, ctx, pool); len(rows) != 0 {
		t.Errorf("outbox has %d rows, want 0", len(rows))
	}
}
