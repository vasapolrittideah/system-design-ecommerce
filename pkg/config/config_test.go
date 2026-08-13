package config_test

import (
	"testing"
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
)

type testConfig struct {
	Addr     string        `env:"ADDR" envDefault:":50051"`
	Timeout  time.Duration `env:"TIMEOUT" envDefault:"5s"`
	Replicas int           `env:"REPLICAS"`
	Secret   string        `env:"SECRET,required"`
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		environ map[string]string
		opts    []config.Option
		want    testConfig
		wantErr bool
	}{
		{
			name:    "defaults apply when unset",
			environ: map[string]string{"SECRET": "s3cret"},
			want:    testConfig{Addr: ":50051", Timeout: 5 * time.Second, Secret: "s3cret"},
		},
		{
			name: "environment overrides defaults",
			environ: map[string]string{
				"ADDR":     ":9090",
				"TIMEOUT":  "250ms",
				"REPLICAS": "3",
				"SECRET":   "s3cret",
			},
			want: testConfig{Addr: ":9090", Timeout: 250 * time.Millisecond, Replicas: 3, Secret: "s3cret"},
		},
		{
			name:    "prefix scopes lookups",
			environ: map[string]string{"ORDER_ADDR": ":7070", "ORDER_SECRET": "s3cret"},
			opts:    []config.Option{config.WithPrefix("ORDER_")},
			want:    testConfig{Addr: ":7070", Timeout: 5 * time.Second, Secret: "s3cret"},
		},
		{
			name:    "missing required variable fails",
			environ: map[string]string{"ADDR": ":9090"},
			wantErr: true,
		},
		{
			name:    "malformed value fails",
			environ: map[string]string{"SECRET": "s3cret", "REPLICAS": "many"},
			wantErr: true,
		},
		{
			name:    "required if no default fails on unset field",
			environ: map[string]string{"SECRET": "s3cret"},
			opts:    []config.Option{config.WithRequiredIfNoDef()},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := append([]config.Option{config.WithEnviron(tt.environ)}, tt.opts...)

			got, err := config.Load[testConfig](opts...)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() = %+v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
			if got != tt.want {
				t.Errorf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadReturnsZeroValueOnError(t *testing.T) {
	got, err := config.Load[testConfig](config.WithEnviron(map[string]string{}))
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if got != (testConfig{}) {
		t.Errorf("Load() = %+v, want zero value", got)
	}
}

func TestMustLoad(t *testing.T) {
	environ := map[string]string{"SECRET": "s3cret", "REPLICAS": "3"}

	got := config.MustLoad[testConfig](config.WithEnviron(environ))

	want := testConfig{Addr: ":50051", Timeout: 5 * time.Second, Replicas: 3, Secret: "s3cret"}
	if got != want {
		t.Errorf("MustLoad() = %+v, want %+v", got, want)
	}
}

func TestMustLoadPanicsOnError(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustLoad() did not panic on invalid configuration")
		}
	}()

	config.MustLoad[testConfig](config.WithEnviron(map[string]string{}))
}
