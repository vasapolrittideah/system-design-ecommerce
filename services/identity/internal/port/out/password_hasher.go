package out

import "github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"

// PasswordHasher turns a plaintext password into something safe to store.
//
// A port rather than a plain function because the algorithm and its cost are
// deployment decisions: the encoded hash carries both, so a later adapter can
// verify an old hash while writing new ones with different settings.
type PasswordHasher interface {
	// Hash is deliberately slow — that is the entire feature — so it is called
	// once per registration, never inside a loop or a transaction. Holding a
	// connection open for the tens of milliseconds this takes would spend it on
	// arithmetic.
	Hash(plain string) (domain.PasswordHash, error)

	// Verify reports whether plain is the password behind hash.
	//
	// A hash it cannot read costs the same time as one it can, and is a false
	// rather than an error. That is what lets the sign-in path call this for an
	// address that has no account at all: the two answers are indistinguishable
	// by how long they took, and without that the RPC is still a way to ask
	// which addresses are registered — just a slower one than comparing replies.
	//
	// The cost is that a corrupted stored hash reads as a wrong password instead
	// of as a broken row. That is the right trade here, where the alternative
	// leaks something about every account on every request.
	Verify(hash domain.PasswordHash, plain string) (bool, error)
}
