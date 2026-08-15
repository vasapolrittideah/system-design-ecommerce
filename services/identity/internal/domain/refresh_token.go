package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
)

// tokenBytes is the entropy behind a refresh token: 256 bits, which is what
// makes the fast hash below the right choice and a guessing attack not a
// consideration at all.
const tokenBytes = 32

// redactedToken is what every formatting path prints in place of a token value.
const redactedToken = "[REDACTED]"

// TokenValue is the plaintext refresh token, and exists for the few
// microseconds between being minted and being handed to the caller.
//
// It redacts itself the way pkg/config.Secret does, and for the same reason: a
// bearer credential that reaches a log backend is valid there for as long as it
// would have been valid anywhere, and the leak is never a reviewed line — it is
// one zap.Any of a struct that happens to contain it while someone is chasing
// an unrelated failure. Reading it is Reveal, so a grep for that name lists
// every place a token value is actually used.
//
// The aggregate never holds one. It holds the hash, so there is no path from a
// stored RefreshToken to the string that opens it.
type TokenValue string

// Reveal returns the underlying value, and is the only way to obtain it.
func (v TokenValue) Reveal() string { return string(v) }

// String covers fmt's %v and %s, including when the value is a field of a
// struct being printed whole.
func (v TokenValue) String() string { return redactedToken }

// GoString covers %#v, which ignores String entirely.
func (v TokenValue) GoString() string { return redactedToken }

// MarshalText covers encoding/json, and with it zap.Any and zap.Reflect.
func (v TokenValue) MarshalText() ([]byte, error) { return []byte(redactedToken), nil }

// TokenHash is the SHA-256 of a token value, and is what the service stores.
//
// A fast hash where passwords use argon2id, which is not an inconsistency: the
// input here is 256 bits from a CSPRNG, so there is no dictionary to run and
// nothing a slow hash would buy. It also has to be deterministic, because the
// lookup is by exact value — a per-row salt would make finding the row
// impossible.
//
// A fixed-size array rather than a slice, so "this is a SHA-256" is the type
// rather than a comment, and two hashes compare with ==.
type TokenHash [sha256.Size]byte

// HashRefreshToken is how a presented token becomes a lookup key.
func HashRefreshToken(value string) TokenHash {
	return sha256.Sum256([]byte(value))
}

// RefreshTokenID identifies one link in a rotation chain.
type RefreshTokenID string

// String returns the canonical form.
func (id RefreshTokenID) String() string { return string(id) }

// FamilyID identifies the chain itself: every token minted by rotating another
// carries the id its predecessor did.
//
// It is what makes theft detectable. A stolen token and the real one refresh
// into the same family, so revoking the family ends the session for both — and
// only the owner can sign in again.
type FamilyID string

// String returns the canonical form.
func (id FamilyID) String() string { return string(id) }

// errInvalidTTL is deliberately unclassified, so it resolves to Internal. A
// non-positive TTL is a misconfigured process rather than a bad request, and
// answering the caller 400 for it would point the investigation at them.
var errInvalidTTL = errors.New("refresh token ttl must be positive")

// RefreshToken is one issued refresh token: a hash, the chain it belongs to,
// when the chain ends, and whether this link has been spent.
type RefreshToken struct {
	id       RefreshTokenID
	userID   UserID
	hash     TokenHash
	familyID FamilyID

	expiresAt time.Time

	// revokedAt is the zero time while the token can still be presented. It is
	// set when the token is spent by a rotation, revoked by a logout, or caught
	// in a family revocation — three causes the schema deliberately does not
	// distinguish, because nothing acts on the difference.
	revokedAt time.Time

	createdAt time.Time
	updatedAt time.Time
	version   int
}

// IssueRefreshToken mints a token at the head of a new chain, returning the
// aggregate to store and the plaintext to hand back exactly once.
//
// The two come back separately because they have different destinations and
// different lifetimes: one is written to a row, the other is written to a
// response and never seen again. A constructor that returned only the aggregate
// would have to keep the plaintext on it, and then every log line that printed
// a token would print a working credential.
func IssueRefreshToken(userID UserID, ttl time.Duration, now time.Time) (*RefreshToken, TokenValue, error) {
	if ttl <= 0 {
		return nil, "", errInvalidTTL
	}

	value := newTokenValue()

	return &RefreshToken{
		id:        RefreshTokenID(uuid.NewString()),
		userID:    userID,
		hash:      HashRefreshToken(value.Reveal()),
		familyID:  FamilyID(uuid.NewString()),
		expiresAt: now.Add(ttl),
		version:   1,
	}, value, nil
}

// Rotate spends this token and returns its successor.
//
// The successor inherits the family and, critically, the same expiry: rotation
// does not extend the chain's end. There is no TTL parameter here at all, so
// there is nothing to pass that could extend it — a token refreshed every ten
// minutes forever would otherwise outlive every policy meant to bound it.
//
// Rotating a token that cannot be presented fails without producing anything,
// and the spent case is the one that matters: reaching it means the token was
// already exchanged once, so a second holder exists. The caller answers that by
// revoking the family.
func (t *RefreshToken) Rotate(now time.Time) (*RefreshToken, TokenValue, error) {
	if err := t.EnsureUsable(now); err != nil {
		return nil, "", err
	}

	value := newTokenValue()

	successor := &RefreshToken{
		id:        RefreshTokenID(uuid.NewString()),
		userID:    t.userID,
		hash:      HashRefreshToken(value.Reveal()),
		familyID:  t.familyID,
		expiresAt: t.expiresAt,
		version:   1,
	}

	t.revokedAt = now

	return successor, value, nil
}

// Revoke ends this token, and does nothing to one already ended.
//
// Idempotent because logging out is a state the caller wants to reach rather
// than a change they are making: a client retrying after a timeout must not see
// a failure, and the first revocation's timestamp is the true one — the moment
// the token stopped working, not the moment someone asked again.
func (t *RefreshToken) Revoke(now time.Time) {
	if !t.revokedAt.IsZero() {
		return
	}

	t.revokedAt = now
}

// EnsureUsable reports why this token cannot be presented, or nil.
//
// Expiry is checked before revocation so that a chain which ran out while
// revoked still reads as expired: the client's next move is the same either
// way, and the expired answer is the one that does not imply anything happened.
func (t *RefreshToken) EnsureUsable(now time.Time) error {
	// Not After: a token is dead at its expiry instant, not one nanosecond
	// later. The boundary matters because expiresAt is compared against a
	// clock, and "still valid at exactly the deadline" is a rule nobody
	// intended to write.
	if !now.Before(t.expiresAt) {
		return ErrRefreshTokenExpired
	}

	if !t.revokedAt.IsZero() {
		return ErrRefreshTokenRevoked
	}

	return nil
}

// RefreshTokenSnapshot is the aggregate's state as it is stored.
type RefreshTokenSnapshot struct {
	ID        RefreshTokenID
	UserID    UserID
	Hash      TokenHash
	FamilyID  FamilyID
	ExpiresAt time.Time

	// RevokedAt is the zero time for a token that is still live, which the
	// repository maps to NULL and back.
	RevokedAt time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int
}

// ReconstituteRefreshToken rebuilds a token from storage without validating it.
// Whether it can be used is EnsureUsable's question, asked against the current
// time rather than answered once at load.
func ReconstituteRefreshToken(s RefreshTokenSnapshot) *RefreshToken {
	return &RefreshToken{
		id:        s.ID,
		userID:    s.UserID,
		hash:      s.Hash,
		familyID:  s.FamilyID,
		expiresAt: s.ExpiresAt,
		revokedAt: s.RevokedAt,
		createdAt: s.CreatedAt,
		updatedAt: s.UpdatedAt,
		version:   s.Version,
	}
}

// Snapshot returns the state for a repository to persist.
func (t *RefreshToken) Snapshot() RefreshTokenSnapshot {
	return RefreshTokenSnapshot{
		ID:        t.id,
		UserID:    t.userID,
		Hash:      t.hash,
		FamilyID:  t.familyID,
		ExpiresAt: t.expiresAt,
		RevokedAt: t.revokedAt,
		CreatedAt: t.createdAt,
		UpdatedAt: t.updatedAt,
		Version:   t.version,
	}
}

// ID returns this link's identifier.
func (t *RefreshToken) ID() RefreshTokenID { return t.id }

// UserID returns whose session this is.
func (t *RefreshToken) UserID() UserID { return t.userID }

// Hash returns the stored hash, which is the only form of the token the
// aggregate has ever held.
func (t *RefreshToken) Hash() TokenHash { return t.hash }

// FamilyID returns the rotation chain.
func (t *RefreshToken) FamilyID() FamilyID { return t.familyID }

// ExpiresAt returns the chain's fixed end.
func (t *RefreshToken) ExpiresAt() time.Time { return t.expiresAt }

// RevokedAt returns when the token stopped working, or the zero time.
func (t *RefreshToken) RevokedAt() time.Time { return t.revokedAt }

// IsRevoked reports whether the token has been ended by any cause.
func (t *RefreshToken) IsRevoked() bool { return !t.revokedAt.IsZero() }

// CreatedAt is the zero time until the row has been written.
func (t *RefreshToken) CreatedAt() time.Time { return t.createdAt }

// UpdatedAt is the zero time until the row has been written.
func (t *RefreshToken) UpdatedAt() time.Time { return t.updatedAt }

// Version is the stored optimistic lock.
//
// It is carried rather than bumped here, because the transition this aggregate
// actually needs to make safe is spending a token, and the guard for that is
// `WHERE revoked_at IS NULL` in the repository. That condition is stronger than
// a version match: it says which transition is legal, not merely that nobody
// else has written since the row was read.
func (t *RefreshToken) Version() int { return t.version }

// newTokenValue mints the plaintext.
//
// base64url without padding, so the value survives a header, a URL, and a JSON
// body without re-encoding. crypto/rand.Read is documented never to fail, so
// there is no path here that produces a predictable token.
func newTokenValue() TokenValue {
	var b [tokenBytes]byte
	rand.Read(b[:]) //nolint:errcheck // documented never to return an error

	return TokenValue(base64.RawURLEncoding.EncodeToString(b[:]))
}
