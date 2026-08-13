package logger_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

// baseConfig spells out every field. Config is meant to be filled by pkg/config
// from the environment, so the `envDefault` tags do not apply to a struct
// literal — a partially filled literal would silently log at info level with a
// stacktrace on every line.
func baseConfig() logger.Config {
	return logger.Config{
		Level:           zapcore.InfoLevel,
		Format:          logger.FormatJSON,
		Service:         "order",
		Version:         "1.4.2",
		AddCaller:       true,
		StacktraceLevel: zapcore.ErrorLevel,
	}
}

// decodeLines parses the JSON objects a logger wrote to buf.
func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}

		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("unmarshal log line %q: %v", raw, err)
		}
		lines = append(lines, line)
	}

	return lines
}

func TestNewEmitsServiceIdentity(t *testing.T) {
	var buf bytes.Buffer

	log := logger.MustNew(baseConfig(), logger.WithWriter(&buf))
	log.Info("order created", zap.String("order_id", "o-1"))

	lines := decodeLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}

	line := lines[0]
	for key, want := range map[string]string{
		"service":  "order",
		"version":  "1.4.2",
		"msg":      "order created",
		"level":    "info",
		"order_id": "o-1",
	} {
		if got := line[key]; got != want {
			t.Errorf("field %q = %v, want %q", key, got, want)
		}
	}

	ts, ok := line["ts"].(string)
	if !ok {
		t.Fatalf("field ts = %v, want an RFC3339 string", line["ts"])
	}
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("ts %q is not RFC3339Nano: %v", ts, err)
	}
	if _, ok := line["caller"]; !ok {
		t.Error("caller field missing, want it present when AddCaller is set")
	}
}

func TestNewRespectsLevel(t *testing.T) {
	var buf bytes.Buffer

	cfg := baseConfig()
	cfg.Level = zapcore.WarnLevel

	log := logger.MustNew(cfg, logger.WithWriter(&buf))
	log.Info("dropped")
	log.Warn("kept")

	lines := decodeLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want only the warn line", len(lines))
	}
	if lines[0]["msg"] != "kept" {
		t.Errorf("msg = %v, want %q", lines[0]["msg"], "kept")
	}
}

func TestNewAddCallerDisabled(t *testing.T) {
	var buf bytes.Buffer

	cfg := baseConfig()
	cfg.AddCaller = false

	log := logger.MustNew(cfg, logger.WithWriter(&buf))
	log.Info("no caller")

	if _, ok := decodeLines(t, &buf)[0]["caller"]; ok {
		t.Error("caller field present, want it omitted when AddCaller is false")
	}
}

func TestNewAttachesStacktraceFromConfiguredLevel(t *testing.T) {
	var buf bytes.Buffer

	log := logger.MustNew(baseConfig(), logger.WithWriter(&buf))
	log.Warn("no trace")
	log.Error("with trace")

	lines := decodeLines(t, &buf)
	if _, ok := lines[0]["stacktrace"]; ok {
		t.Error("warn line carries a stacktrace, want none below error level")
	}
	if _, ok := lines[1]["stacktrace"]; !ok {
		t.Error("error line has no stacktrace, want one at error level")
	}
}

func TestNewWithFields(t *testing.T) {
	var buf bytes.Buffer

	log := logger.MustNew(baseConfig(),
		logger.WithWriter(&buf),
		logger.WithFields(zap.String("region", "ap-southeast-1")),
	)
	log.Info("hello")

	if got := decodeLines(t, &buf)[0]["region"]; got != "ap-southeast-1" {
		t.Errorf("region = %v, want %q", got, "ap-southeast-1")
	}
}

func TestNewSamplingDropsRepeatedLines(t *testing.T) {
	var buf bytes.Buffer

	cfg := baseConfig()
	cfg.SamplingInitial = 2
	cfg.SamplingThereafter = 1000

	log := logger.MustNew(cfg, logger.WithWriter(&buf))
	for range 10 {
		log.Info("same message")
	}

	if lines := decodeLines(t, &buf); len(lines) != 2 {
		t.Errorf("got %d lines, want 2 kept by the sampler", len(lines))
	}
}

func TestNewConsoleFormat(t *testing.T) {
	var buf bytes.Buffer

	cfg := baseConfig()
	cfg.Format = "CONSOLE" // case-insensitive

	log := logger.MustNew(cfg, logger.WithWriter(&buf))
	log.Info("human readable")

	out := buf.String()
	if json.Valid([]byte(out)) {
		t.Errorf("output is JSON, want console encoding: %q", out)
	}
	if !strings.Contains(out, "human readable") {
		t.Errorf("output %q does not contain the message", out)
	}
}

func TestNewRejectsUnknownFormat(t *testing.T) {
	cfg := baseConfig()
	cfg.Format = "logfmt"

	if _, err := logger.New(cfg); err == nil {
		t.Fatal("New() error = nil, want an error for an unsupported format")
	}
}

func TestMustNewPanicsOnError(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustNew() did not panic on an unbuildable config")
		}
	}()

	cfg := baseConfig()
	cfg.Format = "logfmt"

	logger.MustNew(cfg)
}

func TestSetGlobalRestores(t *testing.T) {
	var buf bytes.Buffer

	before := zap.L()
	log := logger.MustNew(baseConfig(), logger.WithWriter(&buf))

	restore := logger.SetGlobal(log)
	zap.L().Info("through the global")
	restore()

	if len(decodeLines(t, &buf)) != 1 {
		t.Error("global logger did not write, want SetGlobal to install it")
	}
	if zap.L() != before {
		t.Error("restore() did not put the previous global logger back")
	}
}

func TestSyncSwallowsStdoutSyncError(t *testing.T) {
	// Syncing stdout fails with EINVAL/ENOTTY/EBADF depending on what is on the
	// other end; none of that must surface as a shutdown error.
	if err := logger.Sync(logger.MustNew(baseConfig())); err != nil {
		t.Errorf("Sync() error = %v, want nil for stdout", err)
	}
}

// failingSyncer is a WriteSyncer whose Sync fails for a reason that is not the
// harmless stdout case.
type failingSyncer struct{ bytes.Buffer }

func (f *failingSyncer) Sync() error { return errors.New("disk gone") }

func TestSyncReportsRealError(t *testing.T) {
	log := logger.MustNew(baseConfig(), logger.WithWriter(&failingSyncer{}))

	err := logger.Sync(log)
	if err == nil {
		t.Fatal("Sync() error = nil, want the underlying failure reported")
	}
	if !strings.Contains(err.Error(), "disk gone") {
		t.Errorf("Sync() error = %v, want it to wrap the underlying failure", err)
	}
}

// TestConfigDefaults pins the envDefault tags, since Config is normally filled
// by pkg/config rather than by a struct literal.
func TestConfigDefaults(t *testing.T) {
	cfg, err := config.Load[logger.Config](
		config.WithPrefix("LOG_"),
		config.WithEnviron(map[string]string{"LOG_SERVICE": "order"}),
	)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	want := logger.Config{
		Level:              zapcore.InfoLevel,
		Format:             logger.FormatJSON,
		Service:            "order",
		Version:            "dev",
		AddCaller:          true,
		StacktraceLevel:    zapcore.ErrorLevel,
		SamplingInitial:    100,
		SamplingThereafter: 100,
	}
	if cfg != want {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestConfigRequiresService(t *testing.T) {
	if _, err := config.Load[logger.Config](config.WithEnviron(map[string]string{})); err == nil {
		t.Error("Load() error = nil, want SERVICE to be required")
	}
}

func TestConfigParsesLevelFromEnv(t *testing.T) {
	cfg, err := config.Load[logger.Config](config.WithEnviron(map[string]string{
		"SERVICE": "order",
		"LEVEL":   "debug",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.Level != zapcore.DebugLevel {
		t.Errorf("Level = %v, want debug", cfg.Level)
	}

	if _, err := config.Load[logger.Config](config.WithEnviron(map[string]string{
		"SERVICE": "order",
		"LEVEL":   "chatty",
	})); err == nil {
		t.Error("Load() error = nil, want an invalid level to fail at load time")
	}
}
