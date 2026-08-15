package out

import "github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"

// PasswordHasher turns a plaintext password into something safe to store.
//
// It is a port rather than a function the use case calls directly because the
// cost parameters are deployment configuration and the algorithm outlives none
// of the passwords hashed with it: the encoded hash carries both, so a future
// adapter can verify an old hash while writing new ones with different
// settings.
//
// There is no Verify here yet. It arrives with Login, which is the first caller
// that has a stored hash to compare against.
type PasswordHasher interface {
	// Hash is deliberately slow — that is the entire feature — so it is called
	// once per registration and never inside a loop or a transaction. Holding a
	// PostgreSQL transaction open for the tens of milliseconds this takes would
	// spend a connection on arithmetic.
	Hash(plain string) (domain.PasswordHash, error)
}
