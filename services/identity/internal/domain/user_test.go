package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
)

func TestNewEmail(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  domain.Email
		valid bool
	}{
		{
			name:  "plain address",
			input: "ada@example.com",
			want:  "ada@example.com",
			valid: true,
		},
		{
			// Two rows differing only in case would be two accounts one
			// person cannot tell apart, and the migration's CHECK rejects
			// the row anyway.
			name:  "uppercase is lowered",
			input: "Ada@Example.COM",
			want:  "ada@example.com",
			valid: true,
		},
		{
			name:  "surrounding space is trimmed",
			input: "  ada@example.com\t",
			want:  "ada@example.com",
			valid: true,
		},
		{
			name:  "plus addressing is kept",
			input: "ada+orders@example.com",
			want:  "ada+orders@example.com",
			valid: true,
		},
		{
			name:  "empty",
			input: "",
		},
		{
			name:  "only whitespace",
			input: "   ",
		},
		{
			name:  "no at sign",
			input: "ada.example.com",
		},
		{
			name:  "no local part",
			input: "@example.com",
		},
		{
			name:  "no domain",
			input: "ada@",
		},
		{
			name:  "two at signs",
			input: "ada@ada@example.com",
		},
		{
			name:  "domain without a dot",
			input: "ada@localhost",
		},
		{
			name:  "inner space",
			input: "ada la@example.com",
		},
		{
			name:  "longer than 254 bytes",
			input: strings.Repeat("a", 250) + "@example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.NewEmail(tt.input)

			if !tt.valid {
				if err == nil {
					t.Fatalf("NewEmail(%q) = %q, want an error", tt.input, got)
				}

				var invalid domain.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("NewEmail(%q) error = %T, want domain.ValidationError", tt.input, err)
				}

				// The kind is what errorx maps to InvalidArgument, and a typo
				// answered as Internal is an alert nobody can act on.
				if invalid.ErrorKind() != "invalid_input" {
					t.Errorf("ErrorKind() = %q, want %q", invalid.ErrorKind(), "invalid_input")
				}

				return
			}

			if err != nil {
				t.Fatalf("NewEmail(%q) error = %v, want nil", tt.input, err)
			}

			if got != tt.want {
				t.Errorf("NewEmail(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// The address must never appear in the message: it is the user's, and this
// string is what reaches a log line and a gRPC status.
func TestNewEmailErrorDoesNotEchoTheAddress(t *testing.T) {
	const address = "ada@localhost"

	_, err := domain.NewEmail(address)
	if err == nil {
		t.Fatal("NewEmail() error = nil, want an error")
	}

	if strings.Contains(err.Error(), address) {
		t.Errorf("error %q contains the address", err)
	}
}

func TestNewUserID(t *testing.T) {
	id := domain.NewUserID()

	parsed, err := domain.ParseUserID(id.String())
	if err != nil {
		t.Fatalf("ParseUserID(%q) error = %v, want nil", id, err)
	}

	if parsed != id {
		t.Errorf("ParseUserID(NewUserID()) = %q, want %q", parsed, id)
	}

	// Version 4, RFC 9562 variant. A generator that quietly stopped setting
	// these would still produce something that parses.
	if id[14] != '4' {
		t.Errorf("version nibble = %q, want '4'", id[14])
	}

	if !strings.ContainsRune("89ab", rune(id[19])) {
		t.Errorf("variant nibble = %q, want one of 8, 9, a, b", id[19])
	}

	// Two calls colliding would mean the ids are not random, which no other
	// assertion here would notice.
	if other := domain.NewUserID(); other == id {
		t.Error("NewUserID() returned the same id twice")
	}
}

func TestParseUserID(t *testing.T) {
	const canonical = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

	tests := []struct {
		name  string
		input string
		want  domain.UserID
		valid bool
	}{
		{
			name:  "canonical form",
			input: canonical,
			want:  canonical,
			valid: true,
		},
		{
			name:  "uppercase is lowered",
			input: strings.ToUpper(canonical),
			want:  canonical,
			valid: true,
		},
		{
			name:  "empty",
			input: "",
		},
		{
			name:  "too short",
			input: "6ba7b810-9dad-11d1-80b4-00c04fd430c",
		},
		{
			name:  "too long",
			input: canonical + "0",
		},
		{
			name:  "hyphens in the wrong places",
			input: "6ba7b8109-dad-11d1-80b4-00c04fd430c8",
		},
		{
			name:  "not hexadecimal",
			input: "6ba7b810-9dad-11d1-80b4-00c04fd430cg",
		},
		{
			// A valid UUID to some parsers, but not what this service
			// stores: accepting it would make one user reachable under
			// several ids.
			name:  "braced form",
			input: "{6ba7b810-9dad-11d1-80b4-00c04fd430c8}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.ParseUserID(tt.input)

			if !tt.valid {
				if err == nil {
					t.Fatalf("ParseUserID(%q) = %q, want an error", tt.input, got)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseUserID(%q) error = %v, want nil", tt.input, err)
			}

			if got != tt.want {
				t.Errorf("ParseUserID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewUser(t *testing.T) {
	email := domain.Email("ada@example.com")

	t.Run("registration grants the customer role and nothing else", func(t *testing.T) {
		user, err := domain.NewUser(email, "hash")
		if err != nil {
			t.Fatalf("NewUser() error = %v, want nil", err)
		}

		roles := user.Roles()
		if len(roles) != 1 || roles[0] != domain.RoleCustomer {
			t.Errorf("Roles() = %v, want [%q]", roles, domain.RoleCustomer)
		}
	})

	t.Run("each user gets a distinct id", func(t *testing.T) {
		first, err := domain.NewUser(email, "hash")
		if err != nil {
			t.Fatalf("NewUser() error = %v, want nil", err)
		}

		second, err := domain.NewUser(email, "hash")
		if err != nil {
			t.Fatalf("NewUser() error = %v, want nil", err)
		}

		if first.ID() == second.ID() {
			t.Error("two users share an id")
		}
	})

	t.Run("timestamps are left to the database", func(t *testing.T) {
		user, err := domain.NewUser(email, "hash")
		if err != nil {
			t.Fatalf("NewUser() error = %v, want nil", err)
		}

		if !user.CreatedAt().IsZero() || !user.UpdatedAt().IsZero() {
			t.Errorf("timestamps = %v/%v, want zero before the row exists", user.CreatedAt(), user.UpdatedAt())
		}
	})

	t.Run("an empty hash is refused", func(t *testing.T) {
		// Only reachable from a hasher that returned success and nothing
		// else, which must not become a row no password matches.
		if _, err := domain.NewUser(email, ""); err == nil {
			t.Fatal("NewUser() error = nil, want an error")
		}
	})
}

func TestUserRolesAreCopied(t *testing.T) {
	user, err := domain.NewUser("ada@example.com", "hash")
	if err != nil {
		t.Fatalf("NewUser() error = %v, want nil", err)
	}

	roles := user.Roles()
	roles[0] = "admin"

	if got := user.Roles(); got[0] != domain.RoleCustomer {
		t.Errorf("Roles() = %v after the caller wrote to its copy, want [%q]", got, domain.RoleCustomer)
	}
}

func TestReconstituteKeepsStoredState(t *testing.T) {
	// A row written before a rule existed still has to load: the alternative is
	// a service that cannot read the accounts it created.
	snapshot := domain.UserSnapshot{
		ID:           "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		Email:        "NOT-normalised@example.com",
		PasswordHash: "hash",
		Roles:        []domain.Role{"customer", "beta-tester"},
		Version:      7,
	}

	user := domain.ReconstituteUser(snapshot)

	if user.Email() != snapshot.Email {
		t.Errorf("Email() = %q, want the stored value %q", user.Email(), snapshot.Email)
	}

	if user.Version() != snapshot.Version {
		t.Errorf("Version() = %d, want %d", user.Version(), snapshot.Version)
	}

	if got := user.Snapshot(); got.ID != snapshot.ID || len(got.Roles) != len(snapshot.Roles) {
		t.Errorf("Snapshot() = %+v, want the state it was built from", got)
	}
}
