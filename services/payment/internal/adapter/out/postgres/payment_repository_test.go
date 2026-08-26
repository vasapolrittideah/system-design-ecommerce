package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/events/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres/postgrestest"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/adapter/out/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/port/out"
)

// migrationsDir is the schema. The test applies the same files goose runs in
// the cluster rather than a copy kept next to it, because a copy is a second
// schema that drifts and reports nothing when it does.
const migrationsDir = "../../../../db/migrations"

const (
	userID      = domain.UserID("6f1c0a4e-2b8d-4c1a-9f3e-5d7b8a2c4e60")
	otherUserID = domain.UserID("7a2d1b5f-3c9e-4d2b-8a4f-6e8c9b3d5f71")
	orderID     = domain.OrderID("8b3e2c6a-4d0f-4e3c-9b5a-7f9d0c4e6a82")
	reference   = domain.ProviderReference("pi_3Nk9Xy2eZvKYlo2C0abcdefg")
)

// setup gives each test its own database with the migrations applied.
//
// A real PostgreSQL, never a fake driver: the CHECK constraints, the partial
// unique index that allows one unresolved attempt per order, the FOR UPDATE
// that orders two settlements, and the ON CONFLICT that decides an idempotency
// claim are all the server's behaviour rather than a mock's.
func setup(t *testing.T) (*pgxpool.Pool, *adapter.PaymentRepository, *adapter.IdempotencyStore) {
	t.Helper()

	pool := postgrestest.New(t, upMigrations(t)...)

	return pool, adapter.NewPaymentRepository(pool), adapter.NewIdempotencyStore(pool)
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

func attempt(t *testing.T, order domain.OrderID) *domain.Payment {
	t.Helper()

	built, err := domain.NewPayment(domain.NewPaymentID(), order, userID, thb(t, 99800), "card")
	if err != nil {
		t.Fatalf("NewPayment() error = %v, want nil", err)
	}

	return built
}

func TestCreateAndFindByID(t *testing.T) {
	_, payments, _ := setup(t)
	ctx := context.Background()

	built := attempt(t, orderID)

	created, err := payments.Create(ctx, built)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if created.Status() != domain.StatusPending {
		t.Errorf("status = %q, want %q", created.Status(), domain.StatusPending)
	}
	// The timestamps and the version are the database's, read back off the row.
	if created.CreatedAt().IsZero() || created.UpdatedAt().IsZero() {
		t.Error("timestamps are zero, want the row's own")
	}
	if created.Version() != 1 {
		t.Errorf("version = %d, want 1", created.Version())
	}

	found, err := payments.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}
	if found.ID() != created.ID() || !found.Amount().Equal(created.Amount()) {
		t.Error("the attempt did not survive the round trip")
	}
	if found.Method() != "card" {
		t.Errorf("method = %q, want %q", found.Method(), "card")
	}
}

func TestFindByIDReportsAMissingAttempt(t *testing.T) {
	_, payments, _ := setup(t)

	_, err := payments.FindByID(context.Background(), domain.NewPaymentID())
	if !errors.Is(err, domain.ErrPaymentNotFound) {
		t.Fatalf("FindByID() error = %v, want %v", err, domain.ErrPaymentNotFound)
	}
}

// The guard the idempotency key cannot provide: two genuine submissions for one
// order, each a first use of its own key, both finding the order payable.
func TestCreateRefusesASecondUnresolvedAttemptForOneOrder(t *testing.T) {
	_, payments, _ := setup(t)
	ctx := context.Background()

	if _, err := payments.Create(ctx, attempt(t, orderID)); err != nil {
		t.Fatalf("first Create() error = %v, want nil", err)
	}

	_, err := payments.Create(ctx, attempt(t, orderID))
	if errorx.KindOf(err) != errorx.KindConflict {
		t.Fatalf("second Create() error kind = %v, want %v", errorx.KindOf(err), errorx.KindConflict)
	}
	if errorx.Reason(err) != "PAYMENT_ALREADY_IN_FLIGHT" {
		t.Errorf("reason = %q, want %q", errorx.Reason(err), "PAYMENT_ALREADY_IN_FLIGHT")
	}
}

// A failed attempt drops out of the partial index, which is what lets a
// customer whose card was declined try another one.
func TestCreateAllowsAnotherAttemptAfterOneFailed(t *testing.T) {
	_, payments, _ := setup(t)
	ctx := context.Background()

	first, err := payments.Create(ctx, attempt(t, orderID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if _, err := first.Fail("card_declined"); err != nil {
		t.Fatalf("Fail() error = %v, want nil", err)
	}
	if _, err := payments.Update(ctx, first); err != nil {
		t.Fatalf("Update() error = %v, want nil", err)
	}

	if _, err := payments.Create(ctx, attempt(t, orderID)); err != nil {
		t.Fatalf("second Create() after a decline error = %v, want nil", err)
	}
}

// A succeeded attempt stays in the index: the order has been paid for, and a
// second charge against it is the thing this exists to refuse.
func TestCreateStillRefusesAfterOneSucceeded(t *testing.T) {
	_, payments, _ := setup(t)
	ctx := context.Background()

	first, err := payments.Create(ctx, attempt(t, orderID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if _, err := first.Succeed(reference); err != nil {
		t.Fatalf("Succeed() error = %v, want nil", err)
	}
	if _, err := payments.Update(ctx, first); err != nil {
		t.Fatalf("Update() error = %v, want nil", err)
	}

	if _, err := payments.Create(ctx, attempt(t, orderID)); errorx.KindOf(err) != errorx.KindConflict {
		t.Fatalf("Create() error kind = %v, want %v", errorx.KindOf(err), errorx.KindConflict)
	}
}

func TestUpdateBumpsTheVersionAndWritesTheOutcome(t *testing.T) {
	_, payments, _ := setup(t)
	ctx := context.Background()

	created, err := payments.Create(ctx, attempt(t, orderID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if _, err := created.Succeed(reference); err != nil {
		t.Fatalf("Succeed() error = %v, want nil", err)
	}

	settled, err := payments.Update(ctx, created)
	if err != nil {
		t.Fatalf("Update() error = %v, want nil", err)
	}
	if settled.Status() != domain.StatusSucceeded {
		t.Errorf("status = %q, want %q", settled.Status(), domain.StatusSucceeded)
	}
	if settled.ProviderReference() != reference {
		t.Errorf("provider reference = %q, want %q", settled.ProviderReference(), reference)
	}
	if settled.Version() != 2 {
		t.Errorf("version = %d, want 2", settled.Version())
	}
}

// The loser of a concurrent settlement finds out rather than believing it
// wrote. FOR UPDATE is what usually prevents the race; this is the backstop
// behind it.
func TestUpdateRefusesAStaleVersion(t *testing.T) {
	_, payments, _ := setup(t)
	ctx := context.Background()

	created, err := payments.Create(ctx, attempt(t, orderID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	stale, err := payments.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}

	if _, err := created.Succeed(reference); err != nil {
		t.Fatalf("Succeed() error = %v, want nil", err)
	}
	if _, err := payments.Update(ctx, created); err != nil {
		t.Fatalf("first Update() error = %v, want nil", err)
	}

	// stale still carries version 1, which no row has any more.
	if _, err := stale.Fail("too_late"); err != nil {
		t.Fatalf("Fail() error = %v, want nil", err)
	}

	_, err = payments.Update(ctx, stale)
	if errorx.KindOf(err) != errorx.KindConflict {
		t.Fatalf("second Update() error kind = %v, want %v", errorx.KindOf(err), errorx.KindConflict)
	}
}

func TestFindByOrderIDsReturnsEveryAttempt(t *testing.T) {
	_, payments, _ := setup(t)
	ctx := context.Background()

	otherOrder := domain.OrderID("9c4f3d7b-5e1a-4f4d-ac6b-8a0e1d5f7b93")

	declined, err := payments.Create(ctx, attempt(t, orderID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	if _, err := declined.Fail("card_declined"); err != nil {
		t.Fatalf("Fail() error = %v, want nil", err)
	}
	if _, err := payments.Update(ctx, declined); err != nil {
		t.Fatalf("Update() error = %v, want nil", err)
	}

	if _, err := payments.Create(ctx, attempt(t, orderID)); err != nil {
		t.Fatalf("retry Create() error = %v, want nil", err)
	}
	if _, err := payments.Create(ctx, attempt(t, otherOrder)); err != nil {
		t.Fatalf("other order Create() error = %v, want nil", err)
	}

	found, err := payments.FindByOrderIDs(ctx, []domain.OrderID{orderID, otherOrder})
	if err != nil {
		t.Fatalf("FindByOrderIDs() error = %v, want nil", err)
	}
	if len(found) != 3 {
		t.Fatalf("got %d attempts, want 3 — both against the first order and one against the second", len(found))
	}

	// An order nobody has tried to pay for contributes nothing and is not an
	// error: one such row must not fail the whole screen a caller is
	// assembling.
	none, err := payments.FindByOrderIDs(ctx, []domain.OrderID{domain.OrderID(domain.NewPaymentID())})
	if err != nil {
		t.Fatalf("FindByOrderIDs() error = %v, want nil", err)
	}
	if len(none) != 0 {
		t.Errorf("got %d attempts for an unpaid order, want 0", len(none))
	}
}

// The outbox row and the attempt are written by one transaction, which is the
// whole reason the outbox exists.
func TestSettlingWritesTheEventToTheOutbox(t *testing.T) {
	pool, payments, _ := setup(t)
	ctx := context.Background()
	tx := txmanager.New(pool)

	created, err := payments.Create(ctx, attempt(t, orderID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	// Nothing is announced by starting an attempt: the order is already waiting
	// for payment and knows it.
	if got := outboxRows(t, pool); got != 0 {
		t.Fatalf("outbox holds %d rows after Create, want 0", got)
	}

	if _, err := created.Succeed(reference); err != nil {
		t.Fatalf("Succeed() error = %v, want nil", err)
	}

	err = tx.Do(ctx, func(ctx context.Context) error {
		_, err := payments.Update(ctx, created)

		return err
	})
	if err != nil {
		t.Fatalf("Update() error = %v, want nil", err)
	}

	eventType, topic, payload := lastOutboxRow(t, pool)
	if eventType != "PaymentSucceeded" {
		t.Errorf("event type = %q, want %q", eventType, "PaymentSucceeded")
	}
	if topic != "ecommerce.payment.events.v1" {
		t.Errorf("topic = %q, want the payment topic", topic)
	}

	// The outbox carries the envelope, and the fact travels inside it as an
	// Any — which is what lets the relay and the topic stay ignorant of what
	// any particular event is made of.
	var envelope eventsv1.EventEnvelope
	if err := proto.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if envelope.GetEventType() != "PaymentSucceeded" {
		t.Errorf("envelope event type = %q, want %q", envelope.GetEventType(), "PaymentSucceeded")
	}

	var event eventsv1.PaymentSucceeded
	if err := envelope.GetPayload().UnmarshalTo(&event); err != nil {
		t.Fatalf("unmarshal the fact inside the envelope: %v", err)
	}
	if event.GetOrderId() != orderID.String() {
		t.Errorf("event order id = %q, want %q", event.GetOrderId(), orderID)
	}
	if event.GetProviderReference() != reference.String() {
		t.Errorf("event reference = %q, want %q", event.GetProviderReference(), reference)
	}
	if event.GetAmount().GetAmountMinor() != 99800 {
		t.Errorf("event amount = %d, want 99800", event.GetAmount().GetAmountMinor())
	}
}

// A rolled-back settlement announces nothing. Without this the outbox would be
// telling the order service about money that never moved.
func TestARolledBackSettlementWritesNoEvent(t *testing.T) {
	pool, payments, _ := setup(t)
	ctx := context.Background()
	tx := txmanager.New(pool)

	created, err := payments.Create(ctx, attempt(t, orderID))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	if _, err := created.Succeed(reference); err != nil {
		t.Fatalf("Succeed() error = %v, want nil", err)
	}

	wantErr := errors.New("something after the write failed")

	err = tx.Do(ctx, func(ctx context.Context) error {
		if _, err := payments.Update(ctx, created); err != nil {
			return err
		}

		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Do() error = %v, want %v", err, wantErr)
	}

	if got := outboxRows(t, pool); got != 0 {
		t.Errorf("outbox holds %d rows, want 0", got)
	}

	back, err := payments.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}
	if back.Status() != domain.StatusPending {
		t.Errorf("status = %q, want the settlement rolled back to %q", back.Status(), domain.StatusPending)
	}
}

func TestClaimTakesAKeyOnceAndReadsBackTheWinner(t *testing.T) {
	_, _, idem := setup(t)
	ctx := context.Background()

	first := domain.NewPaymentID()
	hash := []byte("a request")

	claim, taken, err := idem.Claim(ctx, userID, "pay-1", hash, first)
	if err != nil || !taken {
		t.Fatalf("Claim() = %v, %v, want taken", taken, err)
	}
	if claim.PaymentID != first {
		t.Errorf("claim payment id = %q, want %q", claim.PaymentID, first)
	}

	// A second claim must not overwrite the first one's request hash, which is
	// what tells a retry from a key reused for different content.
	claim, taken, err = idem.Claim(ctx, userID, "pay-1", []byte("a different request"), domain.NewPaymentID())
	if err != nil {
		t.Fatalf("Claim() error = %v, want nil", err)
	}
	if taken {
		t.Fatal("Claim() took a key somebody already holds")
	}
	if claim.PaymentID != first || !bytes.Equal(claim.RequestHash, hash) {
		t.Errorf("claim = %+v, want the first one back unchanged", claim)
	}
}

// The key is scoped to the user, so two clients that both send "pay-1" are two
// claims rather than one collision.
func TestClaimIsScopedToTheUser(t *testing.T) {
	_, _, idem := setup(t)
	ctx := context.Background()

	if _, taken, err := idem.Claim(ctx, userID, "pay-1", []byte("x"), domain.NewPaymentID()); err != nil || !taken {
		t.Fatalf("Claim() = %v, %v, want taken", taken, err)
	}
	if _, taken, err := idem.Claim(ctx, otherUserID, "pay-1", []byte("x"), domain.NewPaymentID()); err != nil || !taken {
		t.Fatalf("Claim() for another user = %v, %v, want taken", taken, err)
	}
}

func TestFindReportsAFreeKey(t *testing.T) {
	_, _, idem := setup(t)

	_, found, err := idem.Find(context.Background(), userID, "never-used")
	if err != nil {
		t.Fatalf("Find() error = %v, want nil", err)
	}
	if found {
		t.Error("Find() found a key nobody claimed")
	}
}

// A claim taken by a transaction that then rolls back releases the key, which
// is what makes the claim and the attempt one act rather than two.
func TestARolledBackClaimReleasesTheKey(t *testing.T) {
	pool, _, idem := setup(t)
	ctx := context.Background()
	tx := txmanager.New(pool)

	wantErr := errors.New("the attempt could not be written")

	err := tx.Do(ctx, func(ctx context.Context) error {
		if _, _, err := idem.Claim(ctx, userID, "pay-1", []byte("x"), domain.NewPaymentID()); err != nil {
			return err
		}

		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Do() error = %v, want %v", err, wantErr)
	}

	_, found, err := idem.Find(ctx, userID, "pay-1")
	if err != nil {
		t.Fatalf("Find() error = %v, want nil", err)
	}
	if found {
		t.Error("the key is still claimed after the transaction rolled back")
	}
}

// Compile-time proof that the adapter satisfies the port it is wired to.
var (
	_ out.PaymentRepository = (*adapter.PaymentRepository)(nil)
	_ out.IdempotencyStore  = (*adapter.IdempotencyStore)(nil)
)

func outboxRows(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()

	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM outbox").Scan(&count); err != nil {
		t.Fatalf("count outbox: %v", err)
	}

	return count
}

func lastOutboxRow(t *testing.T, pool *pgxpool.Pool) (eventType, topic string, payload []byte) {
	t.Helper()

	err := pool.QueryRow(context.Background(),
		"SELECT event_type, topic, payload FROM outbox ORDER BY id DESC LIMIT 1",
	).Scan(&eventType, &topic, &payload)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}

	return eventType, topic, payload
}
