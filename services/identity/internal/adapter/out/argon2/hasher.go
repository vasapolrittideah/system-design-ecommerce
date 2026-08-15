// Package argon2 hashes passwords with argon2id. Which algorithm, at which cost,
// is a deployment decision, and this is where it lives.
package argon2

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

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

// Verify reports whether plain is the password behind hash.
//
// The cost parameters come out of the stored string rather than from the
// constants above, which is what lets those constants be raised without
// invalidating a single existing password.
func (h *Hasher) Verify(hash domain.PasswordHash, plain string) (bool, error) {
	params, salt, want, err := decodeHash(string(hash))
	if err != nil {
		// A hash that cannot be read still costs a full derivation, so that
		// signing in as an address with no account takes the same time as
		// getting a password wrong. Skipping it here would leave the timing to
		// answer what the error text carefully does not.
		argon2.IDKey([]byte(plain), make([]byte, saltLen), iterations, memoryKiB, parallelism, keyLen)

		return false, nil //nolint:nilerr // an unreadable hash is a wrong password, by design
	}

	got := argon2.IDKey(
		[]byte(plain),
		salt,
		params.iterations,
		params.memoryKiB,
		params.parallelism,
		// The stored length, not keyLen: a hash written when that constant was
		// different must still verify. decodeHash has already bounded it.
		uint32(len(want)), //nolint:gosec // bounded by maxKeyLen in decodeHash
	)

	// Constant time, because a byte-by-byte comparison that stops at the first
	// difference tells an attacker how much of a guess was right.
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// hashParams are the cost settings a stored hash was produced with.
type hashParams struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
}

// phcFields is the number of parts a PHC string splits into on "$": a leading
// empty one, then the algorithm, version, parameters, salt, and digest.
const phcFields = 6

// The digest lengths a stored hash may claim.
//
// The upper bound is what keeps the length out of the caller's control: it is
// handed to argon2 as the amount of output to derive, so a crafted row asking
// for gigabytes would be an allocation this process makes on request. 64 covers
// every length anything here would plausibly have written.
const (
	minKeyLen = 16
	maxKeyLen = 64
)

// decodeHash pulls the parameters, salt, and digest out of a PHC-encoded
// argon2id hash.
//
// Every failure is the same error. Nothing branches on which part was wrong —
// the one caller answers all of them by pretending the password was simply
// incorrect — so distinguishing them would only invite a caller to leak the
// difference.
func decodeHash(encoded string) (hashParams, []byte, []byte, error) {
	invalid := errors.New("argon2: not a valid argon2id hash")

	fields := strings.Split(encoded, "$")
	if len(fields) != phcFields || fields[0] != "" || fields[1] != "argon2id" {
		return hashParams{}, nil, nil, invalid
	}

	var version int
	if _, err := fmt.Sscanf(fields[2], "v=%d", &version); err != nil || version != argon2.Version {
		return hashParams{}, nil, nil, invalid
	}

	var params hashParams
	if _, err := fmt.Sscanf(
		fields[3], "m=%d,t=%d,p=%d",
		&params.memoryKiB, &params.iterations, &params.parallelism,
	); err != nil {
		return hashParams{}, nil, nil, invalid
	}

	salt, err := base64.RawStdEncoding.DecodeString(fields[4])
	if err != nil {
		return hashParams{}, nil, nil, invalid
	}

	key, err := base64.RawStdEncoding.DecodeString(fields[5])
	if err != nil || len(key) < minKeyLen || len(key) > maxKeyLen {
		return hashParams{}, nil, nil, invalid
	}

	return params, salt, key, nil
}
