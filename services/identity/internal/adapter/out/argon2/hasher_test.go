package argon2_test

import (
	"strings"
	"testing"

	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/out/argon2"
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
