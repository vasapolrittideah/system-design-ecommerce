// Package postgres is the driven adapter for storage: it maps between the
// domain aggregate and the rows sqlc generated, and turns pgx failures into
// classified errors.
//
// It is the only package in the service that knows a user is a row.
package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/out/postgres/sqlc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/out"
)

// uniqueViolation is PostgreSQL's SQLSTATE for a duplicate key.
const uniqueViolation = "23505"

// emailConstraint is the constraint UNIQUE on users.email produces. It is
// matched by name rather than by code alone so that a primary key collision —
// two random UUIDs coming out the same, i.e. a bug — is reported as Internal
// rather than as something the caller did.
const emailConstraint = "users_email_key"

// UserRepository implements the driven port over pgx.
type UserRepository struct {
	pool *pgxpool.Pool
}

var _ out.UserRepository = (*UserRepository)(nil)

// NewUserRepository builds the repository over a pool.
func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

// queries binds sqlc to whichever handle is correct for this call: the
// transaction txmanager put on the context, or the pool when there is none. It
// is what lets the same method run inside a use case's tx.Do and outside one
// without either saying so.
func (r *UserRepository) queries(ctx context.Context) *sqlc.Queries {
	return sqlc.New(txmanager.From(ctx, r.pool))
}

// Create inserts the user and returns the row as stored.
func (r *UserRepository) Create(ctx context.Context, user *domain.User) (*domain.User, error) {
	snapshot := user.Snapshot()

	id, err := uuid.Parse(snapshot.ID.String())
	if err != nil {
		// Unreachable through the domain constructors, which is exactly why it
		// is Internal: reaching it means an aggregate was built with an id this
		// package cannot store.
		return nil, errorx.Wrap(err, errorx.KindInternal, "parse user id")
	}

	row, err := r.queries(ctx).CreateUser(ctx, sqlc.CreateUserParams{
		ID:           id,
		Email:        snapshot.Email.String(),
		PasswordHash: string(snapshot.PasswordHash),
		Roles:        rolesToStrings(snapshot.Roles),
	})
	if err != nil {
		if isConstraintViolation(err, emailConstraint) {
			// New rather than Wrap, because only an Internal message is
			// scrubbed by ToGRPC: wrapping would describe the schema to anyone
			// who can reach the API. Nothing is lost — a violation of this
			// constraint means what the message and reason code already say.
			//
			// No metadata either: the address is the caller's own input, and it
			// would travel from here into logs and traces.
			return nil, errorx.New(errorx.KindConflict, "email is already registered").
				WithReason("EMAIL_ALREADY_REGISTERED")
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "create user")
	}

	return toDomain(&row), nil
}

// FindByID returns one user.
func (r *UserRepository) FindByID(ctx context.Context, id domain.UserID) (*domain.User, error) {
	parsed, err := uuid.Parse(id.String())
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInvalidInput, "parse user id").
			WithReason("INVALID_USER_ID")
	}

	row, err := r.queries(ctx).GetUserByID(ctx, parsed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Not wrapped, for the same reason as the conflict above: the
			// message reaches the client intact, and "no rows in result set" is
			// pgx's vocabulary rather than this service's.
			return nil, errorx.New(errorx.KindNotFound, "user %s not found", id).
				WithReason("USER_NOT_FOUND")
		}

		return nil, errorx.Wrap(err, errorx.KindInternal, "get user %s", id)
	}

	return toDomain(&row), nil
}

// FindByIDs returns the users that exist, and no error for the ones that do
// not.
func (r *UserRepository) FindByIDs(ctx context.Context, ids []domain.UserID) ([]*domain.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	parsed := make([]uuid.UUID, 0, len(ids))

	for _, id := range ids {
		value, err := uuid.Parse(id.String())
		if err != nil {
			return nil, errorx.Wrap(err, errorx.KindInvalidInput, "parse user id").
				WithReason("INVALID_USER_ID")
		}

		parsed = append(parsed, value)
	}

	rows, err := r.queries(ctx).GetUsersByIDs(ctx, parsed)
	if err != nil {
		return nil, errorx.Wrap(err, errorx.KindInternal, "get %d user(s)", len(ids))
	}

	users := make([]*domain.User, 0, len(rows))
	// Indexed rather than ranged by value: a sqlc row is 128 bytes and a batch
	// read is where copying each one starts to be measurable.
	for i := range rows {
		users = append(users, toDomain(&rows[i]))
	}

	return users, nil
}

// toDomain rebuilds the aggregate from a row. It reconstitutes rather than
// constructs: re-running today's validation over yesterday's data is how a
// service loses the ability to read accounts it created itself.
func toDomain(row *sqlc.User) *domain.User {
	roles := make([]domain.Role, 0, len(row.Roles))
	for _, role := range row.Roles {
		roles = append(roles, domain.Role(role))
	}

	return domain.ReconstituteUser(domain.UserSnapshot{
		ID:           domain.UserID(row.ID.String()),
		Email:        domain.Email(row.Email),
		PasswordHash: domain.PasswordHash(row.PasswordHash),
		Roles:        roles,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		Version:      int(row.Version),
	})
}

func rolesToStrings(roles []domain.Role) []string {
	// Not nil: the column is NOT NULL, and pgx encodes a nil slice as NULL
	// rather than as the empty array the DEFAULT would have supplied.
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		out = append(out, string(role))
	}

	return out
}

// isConstraintViolation reports whether err is a unique violation of the named
// constraint.
func isConstraintViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == uniqueViolation && pgErr.ConstraintName == constraint
}
