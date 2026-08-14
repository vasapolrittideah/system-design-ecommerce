package config

// Secret is a configuration value that must never reach a log line, a span
// attribute, or an error message: a signing key, a database password, a
// payment provider's API key.
//
// It parses exactly like a string — its kind is string, so Load fills it from
// the environment with no special handling — and it redacts itself on every
// path a value normally escapes by: fmt's verbs, encoding/json, and zap's
// reflection encoder, which is what zap.Any falls back to for a struct.
//
// It exists because nothing else stops that leak. A private key held in a
// plain string field is one zap.Any("cfg", cfg) — typed during a debugging
// session, deleted the next day — away from being written to stdout and
// shipped to a log backend that keeps it for a year. Nobody reviews that line
// closely, no error is raised, and the only record that it happened is the
// backend nobody greps for their own private key. The field being a type that
// cannot print itself is the only defence that survives being forgotten.
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

// GoString covers %#v, which ignores String entirely and would otherwise print
// the raw value. That verb is worth its own method because it is precisely
// what gets typed when someone wants to see a config struct in full.
func (s Secret) GoString() string {
	return redacted
}

// MarshalText covers encoding/json, which prefers a TextMarshaler when there is
// no MarshalJSON, and with it zap.Any and zap.Reflect: both encode an arbitrary
// value through the JSON encoder.
//
// There is deliberately no UnmarshalText. Round-tripping a config struct
// through JSON would otherwise restore "[REDACTED]" as the value and fail at
// the point of use rather than at the point of the mistake.
func (s Secret) MarshalText() ([]byte, error) {
	return []byte(redacted), nil
}
