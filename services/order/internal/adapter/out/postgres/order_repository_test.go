package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/events/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres/postgrestest"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/adapter/out/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out"
)

// migrationsDir is the schema. The test applies the same files goose runs in
// the cluster rather than a copy kept next to it, because a copy is a second
// schema that drifts and reports nothing when it does.
const migrationsDir = "../../../../db/migrations"

const (
	userID        = domain.UserID("6f1c0a4e-2b8d-4c1a-9f3e-5d7b8a2c4e60")
	otherUserID   = domain.UserID("7a2d1b5f-3c9e-4d2b-8a4f-6e8c9b3d5f71")
	reservationID = domain.ReservationID("8b3e2c6a-4d0f-4e3c-9b5a-7f9d0c4e6a82")
)

// setup gives each test its own database with the migrations applied.
//
// A real PostgreSQL, never a fake driver: the CHECK constraints, the unique
// index on reservation_id, and the ON CONFLICT that decides an idempotency
// claim are the server's behaviour, not a mock's.
func setup(t *testing.T) (*pgxpool.Pool, *adapter.OrderRepository, *adapter.IdempotencyStore) {
	t.Helper()

	pool := postgrestest.New(t, upMigrations(t)...)

	return pool, adapter.NewOrderRepository(pool), adapter.NewIdempotencyStore(pool)
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

		up, _, found := strings.Cut(string(content), "-- +goose Down")
		if !found {
			up = string(content)
		}

		statements = append(statements, strings.ReplaceAll(up, "-- +goose Up", ""))
	}

	return statements
}

func thb(t *testing.T, amountMinor int64) domain.Money {
	t.Helper()

	currency, err := domain.NewCurrencyCode("THB")
	if err != nil {
		t.Fatalf("NewCurrencyCode() error = %v, want nil", err)
	}

	money, err := domain.NewMoney(amountMinor, currency)
	if err != nil {
		t.Fatalf("NewMoney() error = %v, want nil", err)
	}

	return money
}

func newOrder(t *testing.T, user domain.UserID, reservation domain.ReservationID) *domain.Order {
	t.Helper()

	sku, err := domain.NewSKU("SHIRT-BLUE-M")
	if err != nil {
		t.Fatalf("NewSKU() error = %v, want nil", err)
	}

	line, err := domain.NewOrderLine(sku, 2, thb(t, 49900))
	if err != nil {
		t.Fatalf("NewOrderLine() error = %v, want nil", err)
	}

	order, err := domain.NewOrder(domain.NewOrderID(), user, reservation, []domain.OrderLine{line})
	if err != nil {
		t.Fatalf("NewOrder() error = %v, want nil", err)
	}

	return order
}

func countOutbox(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}

	return n
}

func TestCreateStoresTheOrderWithItsLines(t *testing.T) {
	ctx := context.Background()
	_, orders, _ := setup(t)

	created, err := orders.Create(ctx, newOrder(t, userID, reservationID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	// The timestamps are the database's, and the aggregate had none before the
	// insert returned.
	if created.CreatedAt().IsZero() || created.UpdatedAt().IsZero() {
		t.Errorf("timestamps = %v/%v, want the values DEFAULT now() supplied",
			created.CreatedAt(), created.UpdatedAt())
	}

	read, err := orders.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}
	if read.Status() != domain.StatusPendingPayment {
		t.Errorf("status = %q, want %q", read.Status(), domain.StatusPendingPayment)
	}
	if got := read.Total().AmountMinor(); got != 99800 {
		t.Errorf("total = %d, want %d", got, 99800)
	}
	if lines := read.Lines(); len(lines) != 1 || lines[0].Quantity() != 2 {
		t.Errorf("lines = %+v, want the one that was bought", lines)
	}
	// The price is the order's own record, not a view over the catalog.
	if got := read.Lines()[0].UnitPrice().AmountMinor(); got != 49900 {
		t.Errorf("unit price = %d, want the one agreed at checkout", got)
	}
}

func TestCreateAnnouncesTheOrder(t *testing.T) {
	ctx := context.Background()
	pool, orders, _ := setup(t)

	created, err := orders.Create(ctx, newOrder(t, userID, reservationID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	var (
		aggregateID string
		eventType   string
		topic       string
		payload     []byte
	)
	row := `SELECT aggregate_id, event_type, topic, payload FROM outbox`
	if err := pool.QueryRow(ctx, row).Scan(&aggregateID, &eventType, &topic, &payload); err != nil {
		t.Fatalf("read the outbox row: %v", err)
	}

	if eventType != "OrderPlaced" || topic != "ecommerce.order.events.v1" {
		t.Errorf("event = %s on %s, want OrderPlaced on ecommerce.order.events.v1", eventType, topic)
	}
	// The key the relay publishes under, which is what keeps one order's events
	// in the order they were raised.
	if aggregateID != created.ID().String() {
		t.Errorf("aggregate_id = %q, want the order's id %q", aggregateID, created.ID())
	}

	var envelope eventsv1.EventEnvelope
	if err := proto.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	var placed eventsv1.OrderPlaced
	if err := envelope.GetPayload().UnmarshalTo(&placed); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	// The service consumes this back to turn the hold into a sale, so the
	// reservation has to travel with it.
	if placed.GetReservationId() != reservationID.String() {
		t.Errorf("event reservation = %q, want %q", placed.GetReservationId(), reservationID)
	}
	if placed.GetTotal().GetAmountMinor() != 99800 {
		t.Errorf("event total = %d, want %d", placed.GetTotal().GetAmountMinor(), 99800)
	}
}

func TestCreateRollsTheEventBackWithTheOrder(t *testing.T) {
	ctx := context.Background()
	pool, orders, _ := setup(t)
	wantErr := errors.New("the use case changed its mind")

	err := txmanager.New(pool).Do(ctx, func(ctx context.Context) error {
		if _, err := orders.Create(ctx, newOrder(t, userID, reservationID)); err != nil {
			return err
		}

		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Do() error = %v, want %v", err, wantErr)
	}

	// No event survives an order that did not.
	if n := countOutbox(t, ctx, pool); n != 0 {
		t.Errorf("outbox has %d rows, want 0 after the transaction rolled back", n)
	}
}

func TestCreateRefusesTwoOrdersOnOneReservation(t *testing.T) {
	ctx := context.Background()
	_, orders, _ := setup(t)

	if _, err := orders.Create(ctx, newOrder(t, userID, reservationID)); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	// Committing one would commit the other's stock. The unique index is the
	// guard, and it is the schema's rather than a check in Go.
	if _, err := orders.Create(ctx, newOrder(t, otherUserID, reservationID)); err == nil {
		t.Fatal("Create() error = nil, want the reservation to be taken")
	}
}

func TestFindByIDReportsAnOrderNobodyHas(t *testing.T) {
	ctx := context.Background()
	_, orders, _ := setup(t)

	_, err := orders.FindByID(ctx, domain.NewOrderID())
	if !errors.Is(err, domain.ErrOrderNotFound) {
		t.Fatalf("FindByID() error = %v, want ErrOrderNotFound", err)
	}
}

func TestListReturnsOneCustomersOrdersNewestFirst(t *testing.T) {
	ctx := context.Background()
	_, orders, _ := setup(t)

	var mine []domain.OrderID
	for range 3 {
		created, err := orders.Create(ctx, newOrder(t, userID, domain.ReservationID(domain.NewOrderID())))
		if err != nil {
			t.Fatalf("Create() error = %v, want nil", err)
		}
		mine = append(mine, created.ID())
	}
	if _, err := orders.Create(ctx, newOrder(t, otherUserID, domain.ReservationID(domain.NewOrderID()))); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	page, err := orders.List(ctx, out.OrderFilter{UserID: userID, Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	// Somebody else's order is not in this list, and that is the schema's
	// answer rather than a filter the caller remembered to apply.
	if len(page) != len(mine) {
		t.Fatalf("returned %d orders, want %d", len(page), len(mine))
	}
	if page[0].ID() != mine[len(mine)-1] {
		t.Errorf("first order = %q, want the newest %q", page[0].ID(), mine[len(mine)-1])
	}
	// The lines of the whole page come back in one query, not one per order.
	for _, order := range page {
		if len(order.Lines()) != 1 {
			t.Errorf("order %q came back with %d lines, want 1", order.ID(), len(order.Lines()))
		}
	}
}

func TestListPagesWithoutRepeatingOrSkipping(t *testing.T) {
	ctx := context.Background()
	_, orders, _ := setup(t)

	total := 5
	for range total {
		if _, err := orders.Create(ctx, newOrder(t, userID, domain.ReservationID(domain.NewOrderID()))); err != nil {
			t.Fatalf("Create() error = %v, want nil", err)
		}
	}

	seen := map[domain.OrderID]bool{}
	var after *out.OrderCursor

	for range total {
		page, err := orders.List(ctx, out.OrderFilter{UserID: userID, After: after, Limit: 2})
		if err != nil {
			t.Fatalf("List() error = %v, want nil", err)
		}
		if len(page) == 0 {
			break
		}

		for _, order := range page {
			if seen[order.ID()] {
				t.Fatalf("order %q came back on two pages", order.ID())
			}
			seen[order.ID()] = true
		}

		last := page[len(page)-1]
		after = &out.OrderCursor{CreatedAt: last.CreatedAt(), ID: last.ID()}
	}

	if len(seen) != total {
		t.Errorf("paging returned %d of %d orders", len(seen), total)
	}
}

func TestUpdateMovesTheOrderAndBumpsTheVersion(t *testing.T) {
	ctx := context.Background()
	_, orders, _ := setup(t)

	created, err := orders.Create(ctx, newOrder(t, userID, reservationID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if _, err := created.MarkPaid(); err != nil {
		t.Fatalf("MarkPaid() error = %v, want nil", err)
	}

	paid, err := orders.Update(ctx, created)
	if err != nil {
		t.Fatalf("Update() error = %v, want nil", err)
	}

	if paid.Status() != domain.StatusPaid {
		t.Errorf("status = %q, want %q", paid.Status(), domain.StatusPaid)
	}
	if paid.Version() != 2 {
		t.Errorf("version = %d, want 2", paid.Version())
	}
	// The lines come back with it. They are never modified — changing what was
	// bought would change what the customer agreed to — but an order without
	// them is not an order any caller can use.
	if len(paid.Lines()) != 1 {
		t.Errorf("got %d lines, want the order's own 1", len(paid.Lines()))
	}
}

func TestUpdateAnnouncesTheTransition(t *testing.T) {
	ctx := context.Background()
	pool, orders, _ := setup(t)

	created, err := orders.Create(ctx, newOrder(t, userID, reservationID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	if _, err := created.MarkPaid(); err != nil {
		t.Fatalf("MarkPaid() error = %v, want nil", err)
	}
	if _, err := orders.Update(ctx, created); err != nil {
		t.Fatalf("Update() error = %v, want nil", err)
	}

	var (
		eventType string
		payload   []byte
	)
	row := `SELECT event_type, payload FROM outbox ORDER BY id DESC LIMIT 1`
	if err := pool.QueryRow(ctx, row).Scan(&eventType, &payload); err != nil {
		t.Fatalf("read the outbox row: %v", err)
	}

	if eventType != "OrderPaid" {
		t.Fatalf("event type = %q, want %q", eventType, "OrderPaid")
	}

	var envelope eventsv1.EventEnvelope
	if err := proto.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	var paid eventsv1.OrderPaid
	if err := envelope.GetPayload().UnmarshalTo(&paid); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	// This service consumes OrderPaid back to commit the hold, so the
	// reservation has to travel with it.
	if paid.GetReservationId() != reservationID.String() {
		t.Errorf("event reservation = %q, want %q", paid.GetReservationId(), reservationID)
	}
}

// The loser of a concurrent transition finds out rather than believing it
// wrote. FOR UPDATE is what usually prevents the race; this is the backstop
// behind it.
func TestUpdateRefusesAStaleVersion(t *testing.T) {
	ctx := context.Background()
	_, orders, _ := setup(t)

	created, err := orders.Create(ctx, newOrder(t, userID, reservationID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	stale, err := orders.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}

	if _, err := created.MarkPaid(); err != nil {
		t.Fatalf("MarkPaid() error = %v, want nil", err)
	}
	if _, err := orders.Update(ctx, created); err != nil {
		t.Fatalf("first Update() error = %v, want nil", err)
	}

	// stale still carries version 1, which no row has any more.
	if _, err := stale.Cancel(); err != nil {
		t.Fatalf("Cancel() error = %v, want nil", err)
	}

	_, err = orders.Update(ctx, stale)
	if errorx.KindOf(err) != errorx.KindConflict {
		t.Fatalf("second Update() error kind = %v, want %v", errorx.KindOf(err), errorx.KindConflict)
	}
}

// A rolled-back transition announces nothing. Without this the outbox would be
// telling inventory to sell stock for an order that is still pending.
func TestUpdateRollsTheEventBackWithTheTransition(t *testing.T) {
	ctx := context.Background()
	pool, orders, _ := setup(t)

	created, err := orders.Create(ctx, newOrder(t, userID, reservationID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	if _, err := created.MarkPaid(); err != nil {
		t.Fatalf("MarkPaid() error = %v, want nil", err)
	}

	before := countOutbox(t, ctx, pool)
	wantErr := errors.New("something after the write failed")

	err = txmanager.New(pool).Do(ctx, func(ctx context.Context) error {
		if _, err := orders.Update(ctx, created); err != nil {
			return err
		}

		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Do() error = %v, want %v", err, wantErr)
	}

	if got := countOutbox(t, ctx, pool); got != before {
		t.Errorf("outbox has %d rows, want the %d it had before the rollback", got, before)
	}

	back, err := orders.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}
	if back.Status() != domain.StatusPendingPayment {
		t.Errorf("status = %q, want the transition rolled back to %q", back.Status(), domain.StatusPendingPayment)
	}
}

func TestFindByIDForUpdateReturnsTheOrderWithItsLines(t *testing.T) {
	ctx := context.Background()
	pool, orders, _ := setup(t)

	created, err := orders.Create(ctx, newOrder(t, userID, reservationID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	// FOR UPDATE only means anything inside a transaction, which is also the
	// only place the saga calls it from.
	err = txmanager.New(pool).Do(ctx, func(ctx context.Context) error {
		locked, err := orders.FindByIDForUpdate(ctx, created.ID())
		if err != nil {
			return err
		}

		if locked.ID() != created.ID() || locked.Status() != domain.StatusPendingPayment {
			t.Errorf("locked order = %s / %q, want the created one", locked.ID(), locked.Status())
		}
		if len(locked.Lines()) != 1 {
			t.Errorf("got %d lines, want 1", len(locked.Lines()))
		}
		// Loaded from storage, so it carries none of the events its creation
		// raised — the transition about to be made is the only thing that will.
		if pulled := locked.PullEvents(); len(pulled) != 0 {
			t.Errorf("PullEvents() returned %d events, want 0", len(pulled))
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Do() error = %v, want nil", err)
	}
}

func TestFindByIDForUpdateReportsAnOrderNobodyHas(t *testing.T) {
	ctx := context.Background()
	pool, orders, _ := setup(t)

	err := txmanager.New(pool).Do(ctx, func(ctx context.Context) error {
		_, err := orders.FindByIDForUpdate(ctx, domain.NewOrderID())

		return err
	})
	if !errors.Is(err, domain.ErrOrderNotFound) {
		t.Fatalf("FindByIDForUpdate() error = %v, want %v", err, domain.ErrOrderNotFound)
	}
}

// One writer holds the row and the other waits for it, which is what makes the
// second read the first one's outcome instead of racing it. Without FOR UPDATE
// both would read pending_payment and the loser's UPDATE would match zero rows.
func TestFindByIDForUpdateSerialisesTwoWriters(t *testing.T) {
	ctx := context.Background()
	pool, orders, _ := setup(t)

	created, err := orders.Create(ctx, newOrder(t, userID, reservationID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	tx := txmanager.New(pool)
	held := make(chan struct{})
	released := make(chan struct{})
	second := make(chan domain.Status, 1)

	// Released on every exit, including a failure. t.Fatalf ends this
	// goroutine through runtime.Goexit, which runs deferred calls — without
	// one here a failing assertion would leave the first transaction holding
	// its lock forever, and the test would hang instead of reporting.
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }

	defer release()

	go func() {
		_ = tx.Do(ctx, func(ctx context.Context) error {
			order, err := orders.FindByIDForUpdate(ctx, created.ID())
			if err != nil {
				return err
			}
			if _, err := order.MarkPaid(); err != nil {
				return err
			}
			if _, err := orders.Update(ctx, order); err != nil {
				return err
			}

			// The row is locked and written but not yet committed. The reader
			// below is now blocked on it.
			close(held)
			<-released

			return nil
		})
	}()

	<-held

	go func() {
		_ = tx.Do(ctx, func(ctx context.Context) error {
			order, err := orders.FindByIDForUpdate(ctx, created.ID())
			if err != nil {
				return err
			}

			second <- order.Status()

			return nil
		})
	}()

	// Give the second writer long enough to have read, if it were going to.
	select {
	case status := <-second:
		t.Fatalf("the second reader saw %q while the first still held the row, want it blocked", status)
	case <-time.After(200 * time.Millisecond):
	}

	release()

	select {
	case status := <-second:
		if status != domain.StatusPaid {
			t.Errorf("the second reader saw %q, want the first writer's %q", status, domain.StatusPaid)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second reader never unblocked")
	}
}
