package config_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
)

// value is the string every test below looks for in output that should not
// contain it. It is deliberately distinctive: a substring search for it will
// not collide with anything a formatter emits on its own.
const value = "hunter2-not-in-any-log"

type secretConfig struct {
	Addr     string        `env:"ADDR" envDefault:":50051"`
	Password config.Secret `env:"PASSWORD,required"`
}

func TestSecretLoadsFromEnvironment(t *testing.T) {
	cfg, err := config.Load[secretConfig](config.WithEnviron(map[string]string{
		"PASSWORD": value,
	}))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if got := cfg.Password.Reveal(); got != value {
		t.Errorf("Reveal() = %q, want %q", got, value)
	}
	if got := cfg.Addr; got != ":50051" {
		t.Errorf("Addr = %q, want the default — Secret must not disturb sibling fields", got)
	}
}

// TestSecretRedactsEveryFormattingPath is the point of the type. Each case is a
// way a config struct actually reaches a log line, and every one of them was a
// path the plain string field took the value down.
func TestSecretRedactsEveryFormattingPath(t *testing.T) {
	cfg := secretConfig{Addr: ":50051", Password: config.Secret(value)}

	tests := []struct {
		name string
		got  string
	}{
		{"%v on the struct", fmt.Sprintf("%v", cfg)},
		{"%+v on the struct", fmt.Sprintf("%+v", cfg)},
		{"%#v on the struct", fmt.Sprintf("%#v", cfg)},
		// %s on the struct rather than on the field: staticcheck rewrites the
		// direct call to String(), and the verb is the thing under test.
		{"%s on the struct", fmt.Sprintf("%s", cfg)},
		{"%v on the field", fmt.Sprintf("%v", cfg.Password)},
		{"String on the field", cfg.Password.String()},
		{"fmt.Sprint on the struct", fmt.Sprint(cfg)},
		{"errors carrying the struct", fmt.Errorf("connect: %v", cfg).Error()},
		{"json.Marshal", marshal(t, cfg)},
		{"json.Marshal of the field alone", marshal(t, cfg.Password)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.Contains(tt.got, value) {
				t.Errorf("output leaked the secret: %s", tt.got)
			}
			if !strings.Contains(tt.got, "[REDACTED]") {
				t.Errorf("output = %s, want it to contain [REDACTED]", tt.got)
			}
		})
	}
}

// TestSecretRedactsThroughZap pins the specific line this type was written to
// survive: someone adds zap.Any("cfg", cfg) while chasing a startup failure,
// and the log backend keeps whatever it prints for a year.
func TestSecretRedactsThroughZap(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	log := zap.New(core)

	cfg := secretConfig{Addr: ":50051", Password: config.Secret(value)}

	log.Info("starting", zap.Any("cfg", cfg))
	log.Info("starting", zap.Reflect("cfg", cfg))
	log.Info("starting", zap.Any("password", cfg.Password))
	log.Info("starting", zap.String("password", cfg.Password.String()))
	log.Info("starting", zap.Stringer("password", cfg.Password))

	entries := logs.All()
	if len(entries) != 5 {
		t.Fatalf("logged %d entries, want 5", len(entries))
	}

	for i, entry := range entries {
		encoded, err := json.Marshal(entry.ContextMap())
		if err != nil {
			t.Fatalf("marshal entry %d: %v", i, err)
		}
		if strings.Contains(string(encoded), value) {
			t.Errorf("entry %d leaked the secret: %s", i, encoded)
		}
	}
}

// TestSecretHasNoUnmarshalText guards the deliberate omission. Adding
// UnmarshalText would let a config struct round-trip through JSON and come back
// holding "[REDACTED]" as its actual value, which fails at the point of use
// rather than at the point of the mistake.
func TestSecretHasNoUnmarshalText(t *testing.T) {
	var s config.Secret
	if _, ok := any(&s).(interface{ UnmarshalText([]byte) error }); ok {
		t.Error("Secret implements UnmarshalText; see the comment on MarshalText")
	}
}

func marshal(t *testing.T, v any) string {
	t.Helper()

	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	return string(encoded)
}
