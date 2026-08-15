package out

import "github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"

// PasswordHasher turns a plaintext password into something safe to store.
//
// A port rather than a plain function because the algorithm and its cost are
// deployment decisions: the encoded hash carries both, so a later adapter can
// verify an old hash while writing new ones with different settings.
//
// There is no Verify here yet. It arrives with Login, the first caller that has
// a stored hash to compare against.
type PasswordHasher interface {
	// Hash is deliberately slow — that is the entire feature — so it is called
	// once per registration, never inside a loop or a transaction. Holding a
	// connection open for the tens of milliseconds this takes would spend it on
	// arithmetic.
	Hash(plain string) (domain.PasswordHash, error)
}
