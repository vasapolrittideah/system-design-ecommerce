// Package logger builds the one structured logger every service and package in
// this repo writes through.
//
// Logs are JSON on stdout — the process never manages log files, rotation, or
// destinations; the platform collects the stream. Levels, sampling, and format
// come from the environment, so the same binary logs pretty console output on a
// laptop and machine-readable JSON in a cluster without a rebuild.
//
// Typical wiring in cmd/<x>/main.go:
//
//	cfg := config.MustLoad[logger.Config](config.WithPrefix("LOG_"))
//	log := logger.MustNew(cfg)
//	defer logger.Sync(log)
//	logger.SetGlobal(log)
//
// From there, request-scoped code takes its logger off the context with
// From(ctx) and packages with no context reach for zap.L().
package logger

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Format selects the encoder.
const (
	FormatJSON    = "json"
	FormatConsole = "console"
)

// Config is the environment-driven logger configuration. Services load it with
// pkg/config, conventionally under a "LOG_" prefix.
type Config struct {
	// Level is the minimum level that gets written: debug, info, warn, error,
	// dpanic, panic, fatal.
	Level zapcore.Level `env:"LEVEL" envDefault:"info"`

	// Format is "json" for deployed environments or "console" for local
	// development, where colours and aligned columns matter more than parsing.
	Format string `env:"FORMAT" envDefault:"json"`

	// Service names the emitting service and is attached to every line. It has
	// no default because a log stream that cannot be attributed to a service is
	// close to useless once several services share a collector.
	Service string `env:"SERVICE,required"`

	// Version identifies the build, so a spike in errors can be tied to a
	// rollout.
	Version string `env:"VERSION" envDefault:"dev"`

	// AddCaller records the file and line that emitted the line.
	AddCaller bool `env:"ADD_CALLER" envDefault:"true"`

	// StacktraceLevel is the level from which stack traces are attached. Warn
	// and below would bury the useful lines in noise.
	StacktraceLevel zapcore.Level `env:"STACKTRACE_LEVEL" envDefault:"error"`

	// SamplingInitial is how many identical lines per second are kept before
	// sampling kicks in. Zero disables sampling entirely.
	SamplingInitial int `env:"SAMPLING_INITIAL" envDefault:"100"`

	// SamplingThereafter is how many identical lines beyond the initial burst
	// are dropped for each one kept. Without this, one hot error path can
	// saturate the log pipeline and hide everything else.
	SamplingThereafter int `env:"SAMPLING_THEREAFTER" envDefault:"100"`
}

// Option customizes construction beyond what the environment expresses.
type Option func(*options)

type options struct {
	writer io.Writer
	fields []zap.Field
}

// WithWriter sends output somewhere other than stdout. Intended for tests that
// assert on emitted lines.
func WithWriter(w io.Writer) Option {
	return func(o *options) {
		o.writer = w
	}
}

// WithFields attaches static fields to every line, for facts that hold for the
// whole process (region, pod name, instance ID).
func WithFields(fields ...zap.Field) Option {
	return func(o *options) {
		o.fields = append(o.fields, fields...)
	}
}

// New builds a logger from cfg.
func New(cfg Config, opts ...Option) (*zap.Logger, error) {
	o := options{writer: os.Stdout}
	for _, opt := range opts {
		opt(&o)
	}

	encoder, err := newEncoder(cfg.Format)
	if err != nil {
		return nil, err
	}

	core := zapcore.NewCore(encoder, zapcore.AddSync(o.writer), cfg.Level)
	if cfg.SamplingInitial > 0 {
		core = zapcore.NewSamplerWithOptions(core, time.Second, cfg.SamplingInitial, cfg.SamplingThereafter)
	}

	zapOpts := []zap.Option{zap.AddStacktrace(cfg.StacktraceLevel)}
	if cfg.AddCaller {
		zapOpts = append(zapOpts, zap.AddCaller())
	}

	fields := append([]zap.Field{
		zap.String("service", cfg.Service),
		zap.String("version", cfg.Version),
	}, o.fields...)

	return zap.New(core, zapOpts...).With(fields...), nil
}

// MustNew is New for main and bootstrap wiring, where an unbuildable logger
// means the process has no way to report anything and should not start.
func MustNew(cfg Config, opts ...Option) *zap.Logger {
	log, err := New(cfg, opts...)
	if err != nil {
		panic(err)
	}

	return log
}

// SetGlobal installs log as zap's global logger and redirects the standard
// library's log package into it, so third-party code that logs through either
// lands in the same stream. It returns a function that restores the previous
// globals, which tests use to avoid leaking state.
func SetGlobal(log *zap.Logger) (restore func()) {
	undoGlobals := zap.ReplaceGlobals(log)
	undoStdLog := zap.RedirectStdLog(log)

	return func() {
		undoStdLog()
		undoGlobals()
	}
}

// Sync flushes buffered lines. It is meant to be deferred in main.
//
// Syncing stdout when it is a terminal, a pipe, or a container's log stream
// fails on Linux and macOS with EINVAL, ENOTTY, or EBADF depending on what is
// on the other end. None of those say anything about whether the logs were
// written, and reporting them trains people to ignore shutdown errors, so they
// are swallowed here and everything else is returned.
func Sync(log *zap.Logger) error {
	err := log.Sync()
	if err == nil || isBenignSyncError(err) {
		return nil
	}

	return fmt.Errorf("logger: sync: %w", err)
}

func isBenignSyncError(err error) bool {
	return errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.ENOTTY) ||
		errors.Is(err, syscall.EBADF)
}

func newEncoder(format string) (zapcore.Encoder, error) {
	switch strings.ToLower(format) {
	case FormatJSON:
		return zapcore.NewJSONEncoder(encoderConfig(zapcore.LowercaseLevelEncoder)), nil
	case FormatConsole:
		return zapcore.NewConsoleEncoder(encoderConfig(zapcore.CapitalColorLevelEncoder)), nil
	default:
		return nil, fmt.Errorf("logger: unknown format %q, want %q or %q", format, FormatJSON, FormatConsole)
	}
}

func encoderConfig(level zapcore.LevelEncoder) zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    level,
		EncodeTime:     zapcore.RFC3339NanoTimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
}
