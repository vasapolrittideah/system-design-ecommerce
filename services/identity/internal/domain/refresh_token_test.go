package domain_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
)

const (
	userID = domain.UserID("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	ttl    = 30 * 24 * time.Hour
)

// now is fixed rather than time.Now(): every rule here is about an instant
// relative to the expiry, and a test that reads the clock proves nothing about
// the boundary it is meant to pin.
var now = time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

func issue(t *testing.T) (*domain.RefreshToken, domain.TokenValue) {
	t.Helper()

	token, value, err := domain.IssueRefreshToken(userID, ttl, now)
	if err != nil {
		t.Fatalf("IssueRefreshToken() error = %v, want nil", err)
	}

	return token, value
}

func TestIssueRefreshToken(t *testing.T) {
	token, value := issue(t)

	t.Run("the value is 256 bits of base64url", func(t *testing.T) {
		raw, err := base64.RawURLEncoding.DecodeString(value.Reveal())
		if err != nil {
			t.Fatalf("decode token: %v", err)
		}

		if len(raw) != 32 {
			t.Errorf("token entropy = %d bytes, want 32", len(raw))
		}
	})

	t.Run("the aggregate stores the hash and never the value", func(t *testing.T) {
		if token.Hash() != domain.HashRefreshToken(value.Reveal()) {
			t.Error("Hash() does not match the value that was returned")
		}

		// %+v is how a struct reaches a log line. This fails the day someone
		// adds the plaintext to the aggregate.
		if rendered := fmt.Sprintf("%+v", token.Snapshot()); strings.Contains(rendered, value.Reveal()) {
			t.Errorf("snapshot %q contains the plaintext", rendered)
		}
	})

	t.Run("it is usable and its chain ends at now plus the ttl", func(t *testing.T) {
		if err := token.EnsureUsable(now); err != nil {
			t.Errorf("EnsureUsable() = %v, want nil", err)
		}

		if got := token.ExpiresAt(); !got.Equal(now.Add(ttl)) {
			t.Errorf("ExpiresAt() = %v, want %v", got, now.Add(ttl))
		}
	})

	t.Run("two tokens share nothing", func(t *testing.T) {
		other, otherValue := issue(t)

		if other.ID() == token.ID() {
			t.Error("two tokens share an id")
		}

		if other.FamilyID() == token.FamilyID() {
			t.Error("two independently issued tokens share a family")
		}

		if otherValue == value {
			t.Error("two tokens share a value")
		}
	})

	t.Run("a non-positive ttl is refused as a misconfiguration", func(t *testing.T) {
		// Unclassified on purpose, so it resolves to Internal: nothing the
		// caller sent could cause it.
		_, _, err := domain.IssueRefreshToken(userID, 0, now)
		if err == nil {
			t.Fatal("IssueRefreshToken() error = nil, want an error")
		}

		var validation domain.ValidationError
		if errors.As(err, &validation) {
			t.Error("a bad ttl is reported as invalid input, want an unclassified failure")
		}
	})
}

func TestTokenValueRedactsItself(t *testing.T) {
	// The one credential in this service handed to a client as a bearer string,
	// so every path that could print it has to be covered.
	_, value := issue(t)

	// The verbs are a table rather than literals because %s against a Stringer
	// written inline is a vet finding, and rewriting it to satisfy the linter
	// would stop testing the verb.
	for _, verb := range []string{"%v", "%s", "%+v", "%#v", "%q"} {
		if rendered := fmt.Sprintf(verb, value); strings.Contains(rendered, value.Reveal()) {
			t.Errorf("formatted with %s it printed %q", verb, rendered)
		}
	}

	if rendered := fmt.Sprint(value); strings.Contains(rendered, value.Reveal()) {
		t.Errorf("fmt.Sprint printed %q", rendered)
	}

	// It has to survive being a field of a struct printed whole, which is how
	// it would actually escape.
	holder := struct{ Token domain.TokenValue }{Token: value}
	if rendered := fmt.Sprintf("%+v", holder); strings.Contains(rendered, value.Reveal()) {
		t.Errorf("nested in a struct it printed %q", rendered)
	}

	if text, err := value.MarshalText(); err != nil || strings.Contains(string(text), value.Reveal()) {
		t.Errorf("MarshalText() = %q, %v, want it redacted", text, err)
	}
}

func TestRotate(t *testing.T) {
	t.Run("the successor inherits the family and the expiry", func(t *testing.T) {
		token, value := issue(t)

		successor, newValue, err := token.Rotate(now.Add(time.Hour))
		if err != nil {
			t.Fatalf("Rotate() error = %v, want nil", err)
		}

		if successor.FamilyID() != token.FamilyID() {
			t.Error("the successor started a new family, want the same chain")
		}

		// The whole point: refreshing does not buy more time. A chain that
		// could extend itself would never expire.
		if !successor.ExpiresAt().Equal(token.ExpiresAt()) {
			t.Errorf("successor expires at %v, want the chain's end %v",
				successor.ExpiresAt(), token.ExpiresAt())
		}

		if successor.ID() == token.ID() || newValue == value {
			t.Error("the successor reused its predecessor's id or value")
		}

		if successor.UserID() != token.UserID() {
			t.Error("the successor belongs to a different user")
		}
	})

	t.Run("the predecessor is spent by the rotation", func(t *testing.T) {
		token, _ := issue(t)
		at := now.Add(time.Hour)

		if _, _, err := token.Rotate(at); err != nil {
			t.Fatalf("Rotate() error = %v, want nil", err)
		}

		if !token.IsRevoked() {
			t.Fatal("the rotated token is still usable, want it spent")
		}

		if !token.RevokedAt().Equal(at) {
			t.Errorf("RevokedAt() = %v, want %v", token.RevokedAt(), at)
		}
	})

	t.Run("rotating a spent token is the theft signal", func(t *testing.T) {
		// Two holders of one token: whichever presents it second lands here,
		// and the caller answers by revoking the family.
		token, _ := issue(t)

		if _, _, err := token.Rotate(now.Add(time.Hour)); err != nil {
			t.Fatalf("Rotate() error = %v, want nil", err)
		}

		_, _, err := token.Rotate(now.Add(2 * time.Hour))
		if !errors.Is(err, domain.ErrRefreshTokenRevoked) {
			t.Errorf("Rotate() error = %v, want ErrRefreshTokenRevoked", err)
		}
	})

	t.Run("rotating an expired token produces nothing", func(t *testing.T) {
		token, _ := issue(t)

		successor, value, err := token.Rotate(now.Add(ttl))
		if !errors.Is(err, domain.ErrRefreshTokenExpired) {
			t.Errorf("Rotate() error = %v, want ErrRefreshTokenExpired", err)
		}

		if successor != nil || value != "" {
			t.Error("a failed rotation returned a token")
		}

		// And it does not spend the token on the way out, or a clock skew
		// would silently end a session that was about to be refreshed.
		if token.IsRevoked() {
			t.Error("a failed rotation revoked the token")
		}
	})
}

func TestEnsureUsable(t *testing.T) {
	tests := []struct {
		name    string
		at      time.Time
		revoked bool
		want    error
	}{
		{
			name: "live",
			at:   now.Add(time.Hour),
		},
		{
			// The boundary: dead at the expiry instant, not one nanosecond
			// after it.
			name: "at the expiry instant",
			at:   now.Add(ttl),
			want: domain.ErrRefreshTokenExpired,
		},
		{
			name: "one nanosecond before the expiry instant",
			at:   now.Add(ttl - time.Nanosecond),
		},
		{
			name:    "revoked",
			at:      now.Add(time.Hour),
			revoked: true,
			want:    domain.ErrRefreshTokenRevoked,
		},
		{
			// Expiry wins, so a chain that ran out while revoked reads as
			// expired — the client's next move is the same, and this answer
			// implies nothing happened to the session.
			name:    "revoked and expired",
			at:      now.Add(ttl),
			revoked: true,
			want:    domain.ErrRefreshTokenExpired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, _ := issue(t)
			if tt.revoked {
				token.Revoke(now.Add(time.Minute))
			}

			err := token.EnsureUsable(tt.at)
			if !errors.Is(err, tt.want) {
				t.Errorf("EnsureUsable() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRevokeIsIdempotent(t *testing.T) {
	// A client retrying a logout after a timeout must not see a failure, and
	// the first revocation is the true one: the moment the token stopped
	// working, not the moment someone asked again.
	token, _ := issue(t)

	first := now.Add(time.Hour)
	token.Revoke(first)
	token.Revoke(now.Add(2 * time.Hour))

	if !token.RevokedAt().Equal(first) {
		t.Errorf("RevokedAt() = %v, want the first revocation at %v", token.RevokedAt(), first)
	}
}

func TestRefreshTokenSentinelsAreUnauthenticated(t *testing.T) {
	// Unauthenticated rather than PermissionDenied: a 403 for an expired token
	// makes a frontend log the user out instead of refreshing.
	for _, err := range []error{
		domain.ErrInvalidCredentials,
		domain.ErrRefreshTokenExpired,
		domain.ErrRefreshTokenRevoked,
	} {
		var kinder interface{ ErrorKind() string }
		if !errors.As(err, &kinder) {
			t.Fatalf("%v does not declare a kind", err)
		}

		// Pinned to the constant rather than the literal the sentinels carry:
		// domain cannot import errorx, so a kind that stops matching would
		// otherwise resolve to Internal with nothing reporting it.
		want := string(errorx.KindUnauthenticated)
		if got := kinder.ErrorKind(); got != want {
			t.Errorf("%v declares kind %q, want %q", err, got, want)
		}
	}
}

func TestReconstituteRefreshTokenRoundTrips(t *testing.T) {
	token, _ := issue(t)
	token.Revoke(now.Add(time.Hour))

	got := domain.ReconstituteRefreshToken(token.Snapshot()).Snapshot()

	if got != token.Snapshot() {
		t.Errorf("Snapshot() = %+v, want %+v", got, token.Snapshot())
	}
}
