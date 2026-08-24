// Package domain holds the identity service's aggregates and the rules that
// protect them: who a user is, what makes an email or a password hash valid,
// and which of those rules the database is also holding.
package domain

import (
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Role is what a user is, never what they may do with a particular aggregate —
// that question belongs to whichever service owns it. Roles are copied into the
// "roles" claim when a token is signed, so this set is what every other service
// in the system reasons about.
//
// The migration lists none of them: which roles exist is a rule, and a rule in a
// CHECK constraint makes adding one a migration.
type Role string

// RoleCustomer is what registration grants, and is deliberately the only role
// this package can produce: an account that can administer anything is created
// by an operator, not by anyone who can reach the public API.
const RoleCustomer Role = "customer"

// PasswordHash is an encoded argon2id hash — the algorithm and its parameters
// travel inside the string, so a change of either needs no migration.
//
// It is a distinct type so that a plaintext password cannot be assigned where a
// hash is expected; that mistake is silent at runtime and permanent in the
// database.
type PasswordHash string

// User is the aggregate: a person who can sign in.
//
// Its fields are unexported because two of them are invariants rather than
// data. The email is always normalised, so the UNIQUE constraint and the
// CHECK (email = lower(email)) in the migration hold for every row this package
// can produce; and the password is always a hash, because no constructor
// accepts anything else.
type User struct {
	id           UserID
	email        Email
	passwordHash PasswordHash
	roles        []Role

	// Set by the database on insert and read back, never chosen here: two
	// replicas disagree about the current time by more than the ordering of two
	// registrations is worth.
	createdAt time.Time
	updatedAt time.Time

	// version is the optimistic lock: the value an UPDATE must carry and bump,
	// so a concurrent writer affects zero rows and finds out.
	version int

	// events raised by this instance and not yet taken. They leave through
	// PullEvents, and the repository writes them to the outbox in the
	// transaction that persists the row — which is what makes announcing the
	// change and making it the same act.
	events []Event
}

// NewUser builds a user that has never been persisted.
//
// The identifier is minted here rather than by the database, because the domain
// events an aggregate raises carry that id and are written to the outbox in the
// same transaction as the row.
func NewUser(email Email, passwordHash PasswordHash) (*User, error) {
	if passwordHash == "" {
		return nil, ValidationError{Field: "password", Message: "hash is empty"}
	}

	user := &User{
		id:           NewUserID(),
		email:        email,
		passwordHash: passwordHash,
		roles:        []Role{RoleCustomer},
		version:      1,
	}
	user.events = append(user.events, UserRegistered{
		UserID: user.id,
		Email:  user.email,
		Roles:  cloneRoles(user.roles),
	})

	return user, nil
}

// PullEvents returns the events this instance has raised and forgets them, so
// a repository that persists the aggregate twice does not publish them twice.
//
// A user rebuilt by ReconstituteUser has none: the events belong to the change
// that was just made, not to the state that was read.
func (u *User) PullEvents() []Event {
	pulled := u.events
	u.events = nil

	return pulled
}

// UserSnapshot is the whole state of a user as it is stored, so a repository can
// write a row and rebuild one without the aggregate's fields being exported to
// everything else that imports this package.
//
// It is the persistence shape, not the API shape: what this service tells other
// services about a user is ecommerce.identity.v1.User, which carries neither the
// hash nor the version.
type UserSnapshot struct {
	ID           UserID
	Email        Email
	PasswordHash PasswordHash
	Roles        []Role
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Version      int
}

// ReconstituteUser rebuilds a user from storage.
//
// It validates nothing, and that is deliberate: a rule tightened afterwards must
// not make existing users unreadable. New values come in through the
// constructors, which do validate.
func ReconstituteUser(s UserSnapshot) *User {
	return &User{
		id:           s.ID,
		email:        s.Email,
		passwordHash: s.PasswordHash,
		roles:        s.Roles,
		createdAt:    s.CreatedAt,
		updatedAt:    s.UpdatedAt,
		version:      s.Version,
	}
}

// Snapshot returns the user's state for a repository to persist.
func (u *User) Snapshot() UserSnapshot {
	return UserSnapshot{
		ID:           u.id,
		Email:        u.email,
		PasswordHash: u.passwordHash,
		Roles:        cloneRoles(u.roles),
		CreatedAt:    u.createdAt,
		UpdatedAt:    u.updatedAt,
		Version:      u.version,
	}
}

// ID returns the user's identifier, which is also the "sub" claim of every
// access token issued for them.
func (u *User) ID() UserID { return u.id }

// Email returns the normalised login identity.
func (u *User) Email() Email { return u.email }

// PasswordHash returns the encoded hash. Nothing outside this service ever sees
// it: it is not on the proto message, and the only caller is the sign-in path.
func (u *User) PasswordHash() PasswordHash { return u.passwordHash }

// Roles returns a copy, so a caller ranging over them cannot rewrite the
// aggregate's idea of what this user is.
func (u *User) Roles() []Role { return cloneRoles(u.roles) }

// CreatedAt is the zero time until the row has been written.
func (u *User) CreatedAt() time.Time { return u.createdAt }

// UpdatedAt is the zero time until the row has been written.
func (u *User) UpdatedAt() time.Time { return u.updatedAt }

// Version is the value an UPDATE must carry to win the optimistic lock.
func (u *User) Version() int { return u.version }

// cloneRoles is slices.Clone with a nil result for an empty input, so a user
// with no roles round-trips through Snapshot as nil rather than alternating
// between nil and an empty non-nil slice.
func cloneRoles(roles []Role) []Role {
	if len(roles) == 0 {
		return nil
	}

	return slices.Clone(roles)
}

// UserID is a UUID in its canonical lowercase form.
//
// It is a string rather than a uuid.UUID so that the form stored, the form on
// the proto, and the form compared here are one thing. The conversion that costs
// lives in the repository, which is already translating for pgx.
type UserID string

// NewUserID mints an identifier for a user that has never been stored. It panics
// only if the operating system cannot supply randomness, which is the right
// behaviour: the alternative is a predictable identifier.
func NewUserID() UserID { return UserID(uuid.NewString()) }

// ParseUserID validates an identifier that arrived from outside.
//
// The proto already constrains the field with protovalidate, so a malformed id
// rarely reaches here over gRPC. It is checked anyway because an id that reaches
// the repository unchecked becomes a pgx parse failure — reported as Internal,
// which says the service is broken when the caller made a typo.
func ParseUserID(s string) (UserID, error) {
	invalid := ValidationError{Field: "id", Message: "not a UUID"}

	// The length check is not redundant. uuid.Validate also accepts the braced,
	// urn:uuid:, and unhyphenated spellings, which would make one row reachable
	// under four different keys.
	if len(s) != uuidCanonicalLen {
		return "", invalid
	}

	// Lowercased because that is how PostgreSQL renders a uuid column on the way
	// back out, and an identifier that compares unequal to itself depending on
	// which side of the database it came from is a bug that surfaces far from
	// its cause.
	s = strings.ToLower(s)

	if uuid.Validate(s) != nil {
		return "", invalid
	}

	return UserID(s), nil
}

// uuidCanonicalLen is the length of the 8-4-4-4-12 form.
const uuidCanonicalLen = 36

// String returns the canonical form.
func (id UserID) String() string { return string(id) }

// Email is a login identity, lowercased and trimmed.
//
// The normalisation is what the schema relies on: emails are stored already
// lowercased, so uniqueness is a plain UNIQUE constraint, and
// CHECK (email = lower(email)) fails loudly on a writer that skipped this type
// instead of quietly creating a second account that looks identical to a human.
type Email string

// emailMaxLen is 254 bytes, the longest address SMTP is required to accept, and
// the same bound the proto declares.
const emailMaxLen = 254

// NewEmail normalises and checks an address.
//
// The check is deliberately shallow — non-empty, one "@", something either side
// of it, within the length bound. Deciding whether an address is deliverable by
// looking at it is a well-known way to reject real addresses. Full syntax
// validation is declared once in the proto; what is repeated here is the part
// that protects the schema.
func NewEmail(s string) (Email, error) {
	invalid := ValidationError{Field: "email", Message: "not a valid address"}

	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", ValidationError{Field: "email", Message: "is required"}
	}

	if len(s) > emailMaxLen {
		return "", ValidationError{Field: "email", Message: "is longer than 254 bytes"}
	}

	local, domain, found := strings.Cut(s, "@")
	if !found || local == "" || domain == "" || strings.Contains(domain, "@") {
		return "", invalid
	}

	if !strings.Contains(domain, ".") || strings.ContainsAny(s, " \t\r\n") {
		return "", invalid
	}

	return Email(s), nil
}

// String returns the normalised address.
func (e Email) String() string { return string(e) }
