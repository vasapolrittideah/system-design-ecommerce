package config

// Secret is a configuration value that must never reach a log line, a span
// attribute, or an error message: a signing key, a database password, a
// payment provider's API key.
//
// It parses exactly like a string — its kind is string, so Load fills it with no
// special handling — and redacts itself on every path a value escapes by: fmt's
// verbs, encoding/json, and the reflection encoder zap.Any falls back to.
//
// It exists because the leak is never a reviewed line: it is one
// zap.Any("cfg", cfg) added while chasing a startup failure, raising no error
// and leaving a log backend holding a signing key for a year. A type that cannot
// print itself is the only defence that survives being forgotten.
//
// Reading the value is deliberate and reads that way at the call site:
//
//	key, err := parsePrivateKey(cfg.PrivateKey.Reveal())
//
// which also means a grep for Reveal lists every place in the repo where a
// secret is actually used.
type Secret string

// redacted is what every formatting path prints in place of the value. It is
// bracketed so a redacted value cannot be mistaken for a real one that happens
// to read like a placeholder.
const redacted = "[REDACTED]"

// Reveal returns the underlying value, and is the only way to obtain it.
func (s Secret) Reveal() string {
	return string(s)
}

// String covers fmt's %v and %s, including when the Secret is a field of a
// struct being printed whole — fmt applies Stringer to nested fields too.
func (s Secret) String() string {
	return redacted
}

// GoString covers %#v, which ignores String entirely — and is precisely what
// gets typed when someone wants to see a config struct in full.
func (s Secret) GoString() string {
	return redacted
}

// MarshalText covers encoding/json, and with it zap.Any and zap.Reflect.
//
// There is deliberately no UnmarshalText: round-tripping a config struct through
// JSON would restore "[REDACTED]" as the value and fail at the point of use
// rather than at the point of the mistake.
func (s Secret) MarshalText() ([]byte, error) {
	return []byte(redacted), nil
}
