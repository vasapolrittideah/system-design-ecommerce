// Package reaper is the driving adapter for the timer: it calls the reservation
// sweep on an interval and reports what it did.
//
// It sits beside adapter/in/grpc rather than in cmd/ because it drives the same
// kind of thing a handler does — a use case — and differs only in what asks. The
// use case it calls is deliberately unreachable over gRPC.
package reaper

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/port/in"
)

// Config is the environment-driven reaper configuration, conventionally loaded
// under an "INVENTORY_REAPER_" prefix.
type Config struct {
	// Interval is how long to wait between sweeps that found nothing. It is the
	// worst case for how long a stranded hold keeps capacity off the shelf, on
	// top of the reservation TTL itself.
	Interval time.Duration `env:"INTERVAL" envDefault:"30s"`

	// BatchSize bounds one sweep. Every reservation in a batch holds row locks
	// for the length of the transaction, and those rows are SKUs nobody else can
	// reserve meanwhile — so this trades sweep throughput against how long a
	// popular SKU can be blocked by a sweep.
	BatchSize int `env:"BATCH_SIZE" envDefault:"100"`

	// MaxBackoff caps the wait after a failing sweep. A failure costs nothing
	// but time: the reservations are still expired and still there.
	MaxBackoff time.Duration `env:"MAX_BACKOFF" envDefault:"5m"`
}

// Reaper returns the capacity of holds nobody committed in time.
//
// One instance is the intended deployment. The claim uses FOR UPDATE SKIP
// LOCKED, which is not there to scale the sweep out but so that two pods
// overlapping during a rolling restart divide the backlog instead of one waiting
// behind the other.
type Reaper struct {
	reservations in.ReservationReaper
	metrics      *metrics
	cfg          Config
}

// Option configures a Reaper.
type Option func(*options)

type options struct {
	registerer prometheus.Registerer
}

// WithRegisterer sends the reaper's metrics to a registry other than the default
// one. Every process here passes the registry pkg/observability owns; nothing
// reaches for prometheus.DefaultRegisterer.
func WithRegisterer(reg prometheus.Registerer) Option {
	return func(o *options) { o.registerer = reg }
}

// New builds a reaper over the sweep use case.
func New(reservations in.ReservationReaper, cfg Config, opts ...Option) (*Reaper, error) {
	switch {
	case reservations == nil:
		return nil, errors.New("reaper: needs a reservation sweep")
	case cfg.Interval <= 0:
		return nil, fmt.Errorf("reaper: Interval is %v, want a positive duration", cfg.Interval)
	case cfg.BatchSize < 1:
		return nil, fmt.Errorf("reaper: BatchSize is %d, want at least 1", cfg.BatchSize)
	case cfg.MaxBackoff < cfg.Interval:
		return nil, fmt.Errorf("reaper: MaxBackoff is %v, want at least Interval (%v)", cfg.MaxBackoff, cfg.Interval)
	}

	o := options{registerer: prometheus.DefaultRegisterer}
	for _, opt := range opts {
		opt(&o)
	}

	metrics, err := newMetrics(o.registerer)
	if err != nil {
		return nil, err
	}

	return &Reaper{reservations: reservations, metrics: metrics, cfg: cfg}, nil
}

// Run sweeps until ctx is cancelled, then returns nil.
//
// A sweep that fills its batch is followed immediately by another, so a backlog
// is worked off at the speed of the database rather than of the interval. A
// sweep that fails is retried with a doubling wait.
//
// Errors are logged rather than returned because giving up would strand every
// hold behind the one that failed, and a process that exited would be restarted
// into the same failure. What notices a reaper failing forever is the alert on
// how long stock has been held.
func (r *Reaper) Run(ctx context.Context) error {
	log := logger.From(ctx)
	backoff := r.cfg.Interval

	for {
		swept, err := r.reservations.ExpireReservations(ctx, r.cfg.BatchSize)

		var wait time.Duration

		switch {
		// The sweep was cut short by shutdown, not by a real failure. Its
		// transaction rolled back, so the reservations are still expired and
		// still there for the next process.
		//nolint:nilerr // discarding err here is the point: shutdown is not a failure
		case ctx.Err() != nil:
			return nil
		case err != nil:
			r.metrics.failures.Inc()

			wait = backoff
			backoff = min(backoff*2, r.cfg.MaxBackoff)

			log.Warn("reaper: sweep failed",
				zap.Error(err),
				zap.Duration("retry_in", wait),
			)
		default:
			backoff = r.cfg.Interval

			if swept > 0 {
				r.metrics.expired.Add(float64(swept))
				log.Info("reaper: returned expired stock", zap.Int("reservations", swept))
			}

			if swept == r.cfg.BatchSize {
				// A full batch usually means there is more behind it.
				continue
			}

			wait = r.cfg.Interval
		}

		if !sleep(ctx, wait) {
			return nil
		}
	}
}

// sleep waits for d, and reports false if the context ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
