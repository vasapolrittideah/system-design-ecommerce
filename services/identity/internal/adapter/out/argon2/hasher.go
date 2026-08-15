// Package argon2 hashes passwords with argon2id. Which algorithm, at which cost,
// is a deployment decision, and this is where it lives.
package argon2

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/argon2"

	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/port/out"
)

// The cost parameters, one of the combinations OWASP publishes as equivalent:
// m=9216 KiB, t=4, p=1.
//
// **These are coupled to the memory limit in the Deployment.** Each hash in
// flight allocates memoryKiB, so raising it without raising
// resources.limits.memory (256Mi) turns a burst of registrations into an
// OOMKill — a coupling that reports itself as a restarting pod and never as a
// configuration error.
const (
	memoryKiB   uint32 = 9216
	iterations  uint32 = 4
	parallelism uint8  = 1

	// 16 bytes, the RFC 9106 recommendation. The salt is per-password and
	// stored inside the encoded hash, which is what makes two identical
	// passwords produce different rows.
	saltLen = 16

	// 32 bytes of output. Longer costs storage and answers no attack.
	keyLen uint32 = 32
)

// Hasher implements the password hashing port. It holds no state, so one
// instance is shared by every request and is safe for concurrent use.
type Hasher struct{}

var _ out.PasswordHasher = (*Hasher)(nil)

// NewHasher builds the hasher.
func NewHasher() *Hasher { return &Hasher{} }

// Hash returns a PHC-encoded argon2id hash: the algorithm, its version, the
// cost parameters, and the salt all travel in the string with the digest.
//
// That format is what makes the constants above changeable. A verifier reads the
// parameters out of the stored value rather than assuming today's, so raising
// the cost re-hashes nothing and invalidates nobody's password.
func (h *Hasher) Hash(plain string) (domain.PasswordHash, error) {
	salt := make([]byte, saltLen)
	// Panics internally rather than returning a short read, so there is no path
	// here that produces an unsalted hash.
	rand.Read(salt) //nolint:errcheck // documented never to return an error

	key := argon2.IDKey([]byte(plain), salt, iterations, memoryKiB, parallelism, keyLen)

	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		memoryKiB,
		iterations,
		parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)

	// The error is in the signature for the port, not for this implementation:
	// an adapter that reaches a KMS for a pepper has somewhere to fail.
	return domain.PasswordHash(encoded), nil
}
