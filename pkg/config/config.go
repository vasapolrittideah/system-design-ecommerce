// Package config loads 12-factor configuration from environment variables.
//
// Each service (or package) declares its own struct with `env` tags and calls
// Load with that struct as the type parameter:
//
//	type ServerConfig struct {
//		Addr     string        `env:"ADDR" envDefault:":50051"`
//		Timeout  time.Duration `env:"TIMEOUT" envDefault:"5s"`
//		Password Secret        `env:"PASSWORD,required"`
//	}
//
//	cfg, err := config.Load[ServerConfig](config.WithPrefix("ORDER_GRPC_"))
//
// Configuration is read once at startup and passed down explicitly; nothing
// here reaches for os.Getenv at call time.
//
// Anything that would be damaging in a log line is declared as Secret rather
// than string, which is what keeps a whole config struct safe to print.
package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

// Option customizes how configuration is read.
type Option func(*env.Options)

// WithPrefix prepends prefix to every variable name looked up, letting the same
// struct be reused for several instances of a dependency (e.g. "ORDER_DB_" and
// "READ_DB_").
func WithPrefix(prefix string) Option {
	return func(o *env.Options) {
		o.Prefix = prefix
	}
}

// WithEnviron reads from the given map instead of the process environment.
// Intended for tests, so they never mutate global state.
func WithEnviron(environ map[string]string) Option {
	return func(o *env.Options) {
		o.Environment = environ
	}
}

// WithRequiredIfNoDef treats every field without an `envDefault` tag as
// required, turning a forgotten variable into a startup failure instead of a
// zero value that surfaces much later.
func WithRequiredIfNoDef() Option {
	return func(o *env.Options) {
		o.RequiredIfNoDef = true
	}
}

// Load parses the environment into a freshly allocated T.
//
// All parse failures are reported together, so a misconfigured deployment shows
// every missing or malformed variable in one line rather than one per restart.
func Load[T any](opts ...Option) (T, error) {
	envOpts := env.Options{}
	for _, opt := range opts {
		opt(&envOpts)
	}

	cfg, err := env.ParseAsWithOptions[T](envOpts)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("config: load %T: %w", zero, err)
	}

	return cfg, nil
}

// MustLoad is Load for use in main and bootstrap wiring, where a bad
// configuration means the process cannot start at all.
func MustLoad[T any](opts ...Option) T {
	cfg, err := Load[T](opts...)
	if err != nil {
		panic(err)
	}

	return cfg
}
