// These tests live in the package rather than in postgres_test so they can
// reach poolConfig, the part of New that is worth asserting on without a
// server. Everything past that point needs a real database and belongs to the
// testcontainers-backed tests instead.
package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func testConfig() Config {
	return Config{
		Host:              "db.internal",
		Port:              5432,
		User:              "order_svc",
		Password:          "s3cret",
		Database:          "order",
		SSLMode:           "disable",
		MaxConns:          10,
		MinConns:          2,
		MaxConnLifetime:   30 * time.Minute,
		MaxConnIdleTime:   5 * time.Minute,
		HealthCheckPeriod: time.Minute,
		ConnectTimeout:    5 * time.Second,
	}
}

func TestPoolConfigAppliesConnectionSettings(t *testing.T) {
	cfg := testConfig()

	got, err := poolConfig(cfg)
	if err != nil {
		t.Fatalf("poolConfig() error = %v, want nil", err)
	}

	conn := got.ConnConfig
	if conn.Host != cfg.Host {
		t.Errorf("Host = %q, want %q", conn.Host, cfg.Host)
	}
	if int(conn.Port) != cfg.Port {
		t.Errorf("Port = %d, want %d", conn.Port, cfg.Port)
	}
	if conn.User != cfg.User {
		t.Errorf("User = %q, want %q", conn.User, cfg.User)
	}
	// Compared through Reveal because the config field redacts itself — which
	// is also the assertion that the password survived the round trip through
	// the DSN rather than being written out as "[REDACTED]".
	if conn.Password != cfg.Password.Reveal() {
		t.Errorf("Password = %q, want %q", conn.Password, cfg.Password.Reveal())
	}
	if conn.Database != cfg.Database {
		t.Errorf("Database = %q, want %q", conn.Database, cfg.Database)
	}
	if conn.ConnectTimeout != cfg.ConnectTimeout {
		t.Errorf("ConnectTimeout = %v, want %v", conn.ConnectTimeout, cfg.ConnectTimeout)
	}
	if conn.TLSConfig != nil {
		t.Errorf("TLSConfig = %+v, want nil for sslmode=disable", conn.TLSConfig)
	}
}

func TestPoolConfigAppliesPoolSettings(t *testing.T) {
	cfg := testConfig()

	got, err := poolConfig(cfg)
	if err != nil {
		t.Fatalf("poolConfig() error = %v, want nil", err)
	}

	if got.MaxConns != cfg.MaxConns {
		t.Errorf("MaxConns = %d, want %d", got.MaxConns, cfg.MaxConns)
	}
	if got.MinConns != cfg.MinConns {
		t.Errorf("MinConns = %d, want %d", got.MinConns, cfg.MinConns)
	}
	if got.MaxConnLifetime != cfg.MaxConnLifetime {
		t.Errorf("MaxConnLifetime = %v, want %v", got.MaxConnLifetime, cfg.MaxConnLifetime)
	}
	if got.MaxConnIdleTime != cfg.MaxConnIdleTime {
		t.Errorf("MaxConnIdleTime = %v, want %v", got.MaxConnIdleTime, cfg.MaxConnIdleTime)
	}
	if got.HealthCheckPeriod != cfg.HealthCheckPeriod {
		t.Errorf("HealthCheckPeriod = %v, want %v", got.HealthCheckPeriod, cfg.HealthCheckPeriod)
	}
	if want := cfg.MaxConnLifetime / 10; got.MaxConnLifetimeJitter != want {
		t.Errorf("MaxConnLifetimeJitter = %v, want %v", got.MaxConnLifetimeJitter, want)
	}
}

func TestPoolConfigEscapesCredentials(t *testing.T) {
	cfg := testConfig()
	cfg.User = "order/svc"
	cfg.Password = "p@ss:w/rd?#"

	got, err := poolConfig(cfg)
	if err != nil {
		t.Fatalf("poolConfig() error = %v, want nil", err)
	}

	if got.ConnConfig.User != cfg.User {
		t.Errorf("User = %q, want %q", got.ConnConfig.User, cfg.User)
	}
	if got.ConnConfig.Password != cfg.Password.Reveal() {
		t.Errorf("Password = %q, want %q", got.ConnConfig.Password, cfg.Password.Reveal())
	}
	if got.ConnConfig.Database != cfg.Database {
		t.Errorf("Database = %q, want %q", got.ConnConfig.Database, cfg.Database)
	}
}

func TestPoolConfigRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"zero MaxConns", func(c *Config) { c.MaxConns = 0 }},
		{"negative MaxConns", func(c *Config) { c.MaxConns = -1 }},
		{"negative MinConns", func(c *Config) { c.MinConns = -1 }},
		{"MinConns above MaxConns", func(c *Config) { c.MinConns = 11 }},
		{"unknown sslmode", func(c *Config) { c.SSLMode = "sometimes" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			tt.mutate(&cfg)

			got, err := poolConfig(cfg)
			if err == nil {
				t.Fatalf("poolConfig() = %+v, want error", got)
			}
		})
	}
}

func TestPoolConfigDoesNotLeakPasswordInErrors(t *testing.T) {
	cfg := testConfig()
	cfg.Password = "s3cret"
	cfg.SSLMode = "sometimes"

	_, err := poolConfig(cfg)
	if err == nil {
		t.Fatal("poolConfig() error = nil, want error")
	}
	if strings.Contains(err.Error(), cfg.Password.Reveal()) {
		t.Errorf("error %q contains the password", err)
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	cfg := testConfig()
	cfg.MaxConns = 0

	got, err := New(context.Background(), cfg)
	if err == nil {
		got.Close()
		t.Fatal("New() error = nil, want error")
	}
}

func TestMustNewPanicsOnError(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustNew() did not panic on invalid configuration")
		}
	}()

	cfg := testConfig()
	cfg.MaxConns = 0

	MustNew(context.Background(), cfg)
}

func TestNewRetriesUnreachableDatabase(t *testing.T) {
	cfg := testConfig()
	// Port 1 refuses immediately, which is the same answer a NetworkPolicy the
	// CNI has not yet programmed gives — the case ConnectMaxWait exists for.
	cfg.Host = "127.0.0.1"
	cfg.Port = 1
	cfg.ConnectTimeout = 50 * time.Millisecond
	cfg.ConnectMaxWait = 400 * time.Millisecond

	started := time.Now()

	got, err := New(context.Background(), cfg)
	if err == nil {
		got.Close()
		t.Fatal("New() error = nil, want error")
	}

	if elapsed := time.Since(started); elapsed < cfg.ConnectMaxWait/2 {
		t.Errorf("New() gave up after %v, want it to keep trying for about %v", elapsed, cfg.ConnectMaxWait)
	}
}

func TestNewPingsOnceWithoutConnectMaxWait(t *testing.T) {
	cfg := testConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = 1
	cfg.ConnectTimeout = 50 * time.Millisecond
	cfg.ConnectMaxWait = 0

	started := time.Now()

	got, err := New(context.Background(), cfg)
	if err == nil {
		got.Close()
		t.Fatal("New() error = nil, want error")
	}

	if elapsed := time.Since(started); elapsed > connectBackoff {
		t.Errorf("New() took %v, want a single attempt", elapsed)
	}
}

func TestNewStopsRetryingWhenContextIsCancelled(t *testing.T) {
	cfg := testConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = 1
	cfg.ConnectTimeout = 50 * time.Millisecond
	cfg.ConnectMaxWait = time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	started := time.Now()

	got, err := New(ctx, cfg)
	if err == nil {
		got.Close()
		t.Fatal("New() error = nil, want error")
	}

	if elapsed := time.Since(started); elapsed > cfg.ConnectMaxWait/2 {
		t.Errorf("New() returned after %v, want it to give up with the context", elapsed)
	}
}

// TestNewLogsWhileRetrying pins the line that keeps a slow database from
// looking like a slow process: without it the wait is silent for as long as
// ConnectMaxWait allows, and the only record is a startup that took a while.
func TestNewLogsWhileRetrying(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)

	cfg := testConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = 1
	cfg.ConnectTimeout = 50 * time.Millisecond
	cfg.ConnectMaxWait = 400 * time.Millisecond

	got, err := New(context.Background(), cfg, WithLogger(zap.New(core)))
	if err == nil {
		got.Close()
		t.Fatal("New() error = nil, want error")
	}

	entries := logs.FilterMessage("postgres unreachable, retrying").All()
	if len(entries) == 0 {
		t.Fatal("New() retried without logging, want a line per attempt")
	}

	for i, entry := range entries {
		if entry.Level != zap.WarnLevel {
			t.Errorf("entry %d logged at %v, want warn — the process is still starting", i, entry.Level)
		}

		encoded, err := json.Marshal(entry.ContextMap())
		if err != nil {
			t.Fatalf("marshal entry %d: %v", i, err)
		}
		if strings.Contains(string(encoded), cfg.Password.Reveal()) {
			t.Errorf("entry %d leaked the password: %s", i, encoded)
		}
	}
}
