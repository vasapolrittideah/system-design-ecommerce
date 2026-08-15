package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

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
// A real PostgreSQL, never a fake driver: what is being tested here is a UNIQUE
// violation carrying a constraint name, an array column round-tripping, and
// DEFAULT now() firing — all of them the server's behaviour, none of them a
// mock's.
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

	// ToGRPC replaces the message of an Internal error and no other, so
	// everything in this one reaches the client. Wrapping the pgx failure here
	// would describe the schema to anyone who can call the API.
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
	// This is what lets a use case wrap several repository calls in tx.Do
	// without any of their signatures mentioning PostgreSQL. If the repository
	// reached for the pool instead, the row below would survive the rollback —
	// and the outbox guarantee, which depends on the aggregate and its events
	// sharing one transaction, would be silently gone.
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
