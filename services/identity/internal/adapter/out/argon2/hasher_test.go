package argon2_test

import (
	"strings"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/out/argon2"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
)

const password = "correct horse battery staple"

func TestHashIsPHCEncoded(t *testing.T) {
	// The stored string has to describe how it was made, or raising the cost
	// parameters later invalidates every password already in the table.
	hash, err := argon2.NewHasher().Hash(password)
	if err != nil {
		t.Fatalf("Hash() error = %v, want nil", err)
	}

	fields := strings.Split(string(hash), "$")
	if len(fields) != 6 {
		t.Fatalf("Hash() = %q, want the six-field PHC form", hash)
	}

	if fields[1] != "argon2id" {
		t.Errorf("algorithm = %q, want argon2id", fields[1])
	}

	if fields[2] != "v=19" {
		t.Errorf("version = %q, want v=19", fields[2])
	}

	if !strings.HasPrefix(fields[3], "m=") ||
		!strings.Contains(fields[3], "t=") ||
		!strings.Contains(fields[3], "p=") {
		t.Errorf("parameters = %q, want m=, t= and p=", fields[3])
	}

	if fields[4] == "" || fields[5] == "" {
		t.Errorf("Hash() = %q, want a salt and a digest", hash)
	}
}

func TestHashIsSaltedPerCall(t *testing.T) {
	// Two people choosing the same password must not produce the same row, or
	// one leaked table tells an attacker which accounts to try first.
	hasher := argon2.NewHasher()

	first, err := hasher.Hash(password)
	if err != nil {
		t.Fatalf("Hash() error = %v, want nil", err)
	}

	second, err := hasher.Hash(password)
	if err != nil {
		t.Fatalf("Hash() error = %v, want nil", err)
	}

	if first == second {
		t.Error("Hash() returned the same value for the same password twice")
	}
}

func TestHashDoesNotContainThePassword(t *testing.T) {
	hash, err := argon2.NewHasher().Hash(password)
	if err != nil {
		t.Fatalf("Hash() error = %v, want nil", err)
	}

	if strings.Contains(string(hash), password) {
		t.Errorf("Hash() = %q contains the plaintext", hash)
	}
}

func TestVerify(t *testing.T) {
	hasher := argon2.NewHasher()

	hash, err := hasher.Hash(password)
	if err != nil {
		t.Fatalf("Hash() error = %v, want nil", err)
	}

	t.Run("accepts the password it was made from", func(t *testing.T) {
		matches, err := hasher.Verify(hash, password)
		if err != nil {
			t.Fatalf("Verify() error = %v, want nil", err)
		}

		if !matches {
			t.Error("Verify() = false, want true for the original password")
		}
	})

	t.Run("rejects a different password", func(t *testing.T) {
		matches, err := hasher.Verify(hash, password+"!")
		if err != nil {
			t.Fatalf("Verify() error = %v, want nil", err)
		}

		if matches {
			t.Error("Verify() = true, want false")
		}
	})

	t.Run("reads a hash made with other cost parameters", func(t *testing.T) {
		// The whole reason the parameters are stored in the string: raising the
		// constants must not lock anyone out of an account they can still open.
		// This hash was produced at m=8, t=1, p=1.
		const cheap = "$argon2id$v=19$m=8,t=1,p=1$MDEyMzQ1Njc4OWFiY2RlZg$" +
			"XdTJKPvVpEdCNu922dgwQXj+XfOITS04PpG3cDMn5sE"

		matches, err := hasher.Verify(cheap, password)
		if err != nil {
			t.Fatalf("Verify() error = %v, want nil", err)
		}

		if !matches {
			t.Error("Verify() = false, want true — the stored parameters were not used")
		}
	})

	t.Run("an unreadable hash is a false, never an error", func(t *testing.T) {
		// It is what the sign-in path calls for an address with no account, so
		// that the answer costs the same time as a wrong password. An error
		// would make the use case branch, and a branch is a difference someone
		// can measure.
		for _, hash := range []string{
			"",
			"not-a-hash",
			"$argon2i$v=19$m=9216,t=4,p=1$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZg",
			"$argon2id$v=16$m=9216,t=4,p=1$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZg",
			"$argon2id$v=19$m=x,t=4,p=1$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZg",
			"$argon2id$v=19$m=9216,t=4,p=1$!!!$MDEyMzQ1Njc4OWFiY2RlZg",
		} {
			matches, err := hasher.Verify(domain.PasswordHash(hash), password)
			if err != nil {
				t.Errorf("Verify(%q) error = %v, want nil", hash, err)
			}

			if matches {
				t.Errorf("Verify(%q) = true, want false", hash)
			}
		}
	})
}
