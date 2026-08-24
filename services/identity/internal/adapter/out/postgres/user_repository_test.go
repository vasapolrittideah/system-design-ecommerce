package postgres_test

import (
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
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/out/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
)

// migrationsDir is the schema. The test applies the same files goose runs in
// the cluster rather than a copy kept next to it, because a copy is a second
// schema that drifts and reports nothing when it does.
const migrationsDir = "../../../../db/migrations"

// setup gives each test its own database with the migrations applied.
//
// A real PostgreSQL, never a fake driver: a UNIQUE violation carrying a
// constraint name, an array column round-tripping, and DEFAULT now() firing are
// the server's behaviour, not a mock's.
func setup(t *testing.T) (*pgxpool.Pool, *adapter.UserRepository) {
	t.Helper()

	pool := postgrestest.New(t, upMigrations(t)...)

	return pool, adapter.NewUserRepository(pool)
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

		up, _, _ := strings.Cut(string(content), "-- +goose Down")
		statements = append(statements, up)
	}

	return statements
}

func newUser(t *testing.T, email string) *domain.User {
	t.Helper()

	address, err := domain.NewEmail(email)
	if err != nil {
		t.Fatalf("NewEmail(%q) error = %v", email, err)
	}

	user, err := domain.NewUser(address, "argon2id-hash")
	if err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}

	return user
}

func TestCreateAndFindByID(t *testing.T) {
	ctx := context.Background()
	_, users := setup(t)

	created, err := users.Create(ctx, newUser(t, "ada@example.com"))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	// The timestamps are the database's, and the aggregate had none before the
	// insert returned.
	if created.CreatedAt().IsZero() || created.UpdatedAt().IsZero() {
		t.Errorf("Create() timestamps = %v/%v, want the values DEFAULT now() supplied",
			created.CreatedAt(), created.UpdatedAt())
	}

	if created.Version() != 1 {
		t.Errorf("Create() version = %d, want 1", created.Version())
	}

	found, err := users.FindByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("FindByID() error = %v, want nil", err)
	}

	if found.Email() != "ada@example.com" {
		t.Errorf("FindByID() email = %q, want %q", found.Email(), "ada@example.com")
	}

	if roles := found.Roles(); len(roles) != 1 || roles[0] != domain.RoleCustomer {
		t.Errorf("FindByID() roles = %v, want [%q]", roles, domain.RoleCustomer)
	}

	// The hash round-trips even though it never leaves the service: sign-in is
	// the only reader, and it reads through this method.
	if found.PasswordHash() != "argon2id-hash" {
		t.Errorf("FindByID() hash = %q, want the stored value", found.PasswordHash())
	}
}

func TestCreateDuplicateEmailIsAConflict(t *testing.T) {
	ctx := context.Background()
	_, users := setup(t)

	if _, err := users.Create(ctx, newUser(t, "ada@example.com")); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	_, err := users.Create(ctx, newUser(t, "ada@example.com"))
	if err == nil {
		t.Fatal("Create() error = nil, want a conflict")
	}

	if got := errorx.KindOf(err); got != errorx.KindConflict {
		t.Errorf("KindOf() = %q, want %q", got, errorx.KindConflict)
	}

	// The reason code is API: the Composition API branches on it to tell a
	// duplicate email apart from every other 409.
	if got := errorx.Reason(err); got != "EMAIL_ALREADY_REGISTERED" {
		t.Errorf("Reason() = %q, want %q", got, "EMAIL_ALREADY_REGISTERED")
	}

	// The address is the caller's own input and must not travel back out in
	// metadata, which is copied into logs and traces.
	for key, value := range errorx.Metadata(err) {
		if strings.Contains(value, "ada@example.com") {
			t.Errorf("metadata[%q] leaks the address", key)
		}
	}

	// Every part of a non-Internal message reaches the client, so wrapping the
	// pgx failure would describe the schema to anyone who can call the API.
	for _, leak := range []string{"SQLSTATE", "users_email_key", "duplicate key"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q leaks %q", err, leak)
		}
	}
}

func TestFindByIDMissingIsNotFound(t *testing.T) {
	ctx := context.Background()
	_, users := setup(t)

	_, err := users.FindByID(ctx, domain.NewUserID())
	if err == nil {
		t.Fatal("FindByID() error = nil, want not found")
	}

	if got := errorx.KindOf(err); got != errorx.KindNotFound {
		t.Errorf("KindOf() = %q, want %q", got, errorx.KindNotFound)
	}

	if got := errorx.Reason(err); got != "USER_NOT_FOUND" {
		t.Errorf("Reason() = %q, want %q", got, "USER_NOT_FOUND")
	}

	// "no rows in result set" is pgx's vocabulary, and a NotFound message is
	// passed to the client unchanged.
	if strings.Contains(err.Error(), "no rows") {
		t.Errorf("error %q leaks the driver's message", err)
	}
}

func TestFindByIDsReturnsWhatExists(t *testing.T) {
	ctx := context.Background()
	_, users := setup(t)

	first, err := users.Create(ctx, newUser(t, "ada@example.com"))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	second, err := users.Create(ctx, newUser(t, "grace@example.com"))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	// A missing id in the middle is the ordinary case, not an error: the
	// screen the Composition API is assembling names a user who was deleted.
	found, err := users.FindByIDs(ctx, []domain.UserID{first.ID(), domain.NewUserID(), second.ID()})
	if err != nil {
		t.Fatalf("FindByIDs() error = %v, want nil", err)
	}

	if len(found) != 2 {
		t.Fatalf("FindByIDs() returned %d users, want 2", len(found))
	}
}

func TestCreateJoinsTheAmbientTransaction(t *testing.T) {
	// If the repository reached for the pool instead of the transaction on the
	// context, the row below would survive the rollback — and the outbox
	// guarantee, which needs the aggregate and its events in one transaction,
	// would be silently gone.
	ctx := context.Background()
	pool, users := setup(t)

	transactions := txmanager.New(pool)
	user := newUser(t, "ada@example.com")

	failure := errors.New("compensating")

	err := transactions.Do(ctx, func(ctx context.Context) error {
		if _, err := users.Create(ctx, user); err != nil {
			return err
		}

		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("Do() error = %v, want the failure that rolled it back", err)
	}

	if _, err := users.FindByID(ctx, user.ID()); !errors.Is(err, errorx.ErrNotFound) {
		t.Errorf("FindByID() after rollback = %v, want not found", err)
	}
}

// outboxRow is what the relay will later publish.
type outboxRow struct {
	AggregateType string
	AggregateID   string
	EventType     string
	Topic         string
	Payload       []byte
	Headers       map[string]string
}

func readOutbox(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []outboxRow {
	t.Helper()

	sql := `SELECT aggregate_type, aggregate_id, event_type, topic, payload, headers
	        FROM outbox ORDER BY id`

	rows, err := pool.Query(ctx, sql)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()

	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.AggregateType, &r.AggregateID, &r.EventType, &r.Topic, &r.Payload, &r.Headers); err != nil {
			t.Fatalf("scan outbox row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read outbox rows: %v", err)
	}

	return out
}

func TestCreateWritesTheRegistrationToTheOutbox(t *testing.T) {
	ctx := context.Background()
	pool, users := setup(t)

	created, err := users.Create(ctx, newUser(t, "ada@example.com"))
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	rows := readOutbox(t, ctx, pool)
	if len(rows) != 1 {
		t.Fatalf("outbox has %d rows, want 1", len(rows))
	}

	row := rows[0]
	if row.EventType != "UserRegistered" {
		t.Errorf("event_type = %q, want %q", row.EventType, "UserRegistered")
	}
	if row.Topic != "ecommerce.identity.events.v1" {
		t.Errorf("topic = %q, want %q", row.Topic, "ecommerce.identity.events.v1")
	}
	// The key the relay publishes under, which is what keeps one user's events
	// in order.
	if row.AggregateID != created.ID().String() {
		t.Errorf("aggregate_id = %q, want %q", row.AggregateID, created.ID())
	}

	var envelope eventsv1.EventEnvelope
	if err := proto.Unmarshal(row.Payload, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if envelope.GetEventId() == "" {
		t.Error("envelope event_id is empty, want the id a consumer claims")
	}
	if envelope.GetVersion() != int64(created.Version()) {
		t.Errorf("envelope version = %d, want the row's %d", envelope.GetVersion(), created.Version())
	}
	// The database's clock, not this process's: the aggregate had no timestamp
	// until the insert returned one.
	if got := envelope.GetOccurredAt().AsTime(); !got.Equal(created.CreatedAt().UTC()) {
		t.Errorf("envelope occurred_at = %v, want the row's created_at %v", got, created.CreatedAt().UTC())
	}

	var payload eventsv1.UserRegistered
	if err := envelope.GetPayload().UnmarshalTo(&payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.GetEmail() != "ada@example.com" {
		t.Errorf("payload email = %q, want %q", payload.GetEmail(), "ada@example.com")
	}
	if len(payload.GetRoles()) != 1 || payload.GetRoles()[0] != string(domain.RoleCustomer) {
		t.Errorf("payload roles = %v, want [%v]", payload.GetRoles(), domain.RoleCustomer)
	}
}

func TestCreateAnnouncesNothingWhenTheInsertFails(t *testing.T) {
	ctx := context.Background()
	pool, users := setup(t)

	if _, err := users.Create(ctx, newUser(t, "ada@example.com")); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	if _, err := users.Create(ctx, newUser(t, "ada@example.com")); err == nil {
		t.Fatal("Create() error = nil, want a conflict")
	}

	// The second registration never happened, so nothing may tell the rest of
	// the system that it did.
	if rows := readOutbox(t, ctx, pool); len(rows) != 1 {
		t.Errorf("outbox has %d rows, want 1", len(rows))
	}
}

func TestCreateRollsTheEventBackWithTheUser(t *testing.T) {
	ctx := context.Background()
	pool, users := setup(t)
	wantErr := errors.New("the use case changed its mind")

	err := txmanager.New(pool).Do(ctx, func(ctx context.Context) error {
		if _, err := users.Create(ctx, newUser(t, "ada@example.com")); err != nil {
			return err
		}

		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Do() error = %v, want %v", err, wantErr)
	}

	// The whole point of the outbox: no event survives an aggregate that did
	// not.
	if rows := readOutbox(t, ctx, pool); len(rows) != 0 {
		t.Errorf("outbox has %d rows, want 0 after the transaction rolled back", len(rows))
	}
}
