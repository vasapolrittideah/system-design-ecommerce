package reaper_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/adapter/in/reaper"
)

// sweep is a scripted ReservationReaper. It answers with what the test asked
// for and publishes the limit it was called with, so the assertions are about
// when the loop decided to sweep rather than about a counter the two goroutines
// would otherwise share.
type sweep struct {
	answer func(call int) (int, error)
	calls  chan int

	// Counted separately from the channel, which the test drains: the answer a
	// sweep gives depends on which call it is, not on how far behind the
	// assertions are.
	n atomic.Int64
}

func newSweep(answer func(call int) (int, error)) *sweep {
	return &sweep{answer: answer, calls: make(chan int, 16)}
}

func (s *sweep) ExpireReservations(_ context.Context, limit int) (int, error) {
	call := int(s.n.Add(1))

	// Published without blocking, and dropped once the buffer is full. A sweep
	// that fills its batch is followed immediately by another, so a test that
	// has read the calls it cares about leaves the loop spinning against a
	// channel nobody drains — a blocking send there would hold the reaper inside
	// this call, where it never reaches the context check that stops it, and
	// shutdown would time out rather than the assertion failing.
	select {
	case s.calls <- limit:
	default:
	}

	return s.answer(call)
}

// start runs the reaper and returns the function that stops it and checks how it
// stopped.
func start(t *testing.T, s *sweep, cfg reaper.Config) func() {
	t.Helper()

	r, err := reaper.New(s, cfg, reaper.WithRegisterer(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	stopped := make(chan error, 1)
	go func() { stopped <- r.Run(ctx) }()

	return func() {
		t.Helper()
		cancel()

		select {
		case err := <-stopped:
			// Shutdown is not a failure. A Run that returned the cancellation
			// would make every rolling restart look like a crash.
			if err != nil {
				t.Errorf("Run() error = %v, want nil on shutdown", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run() did not return after the context was cancelled")
		}
	}
}

// waitForSweep reads the next call, failing the test if none arrives.
func waitForSweep(t *testing.T, s *sweep) int {
	t.Helper()

	select {
	case limit := <-s.calls:
		return limit
	case <-time.After(5 * time.Second):
		t.Fatal("the reaper did not sweep")

		return 0
	}
}

func TestRunSweepsAgainImmediatelyOnAFullBatch(t *testing.T) {
	// Every answer fills the batch, so the loop should never reach its wait.
	// The interval is an hour: a test that gets through three sweeps at all is
	// the proof that a full batch skips it.
	s := newSweep(func(int) (int, error) { return 2, nil })

	stop := start(t, s, reaper.Config{Interval: time.Hour, BatchSize: 2, MaxBackoff: time.Hour})
	defer stop()

	for i := range 3 {
		if got := waitForSweep(t, s); got != 2 {
			t.Errorf("sweep %d asked for %d, want the configured batch size 2", i, got)
		}
	}
}

func TestRunWaitsAfterAShortBatch(t *testing.T) {
	// Fewer than the batch means it has caught up, so the next sweep is the
	// interval away — otherwise an idle reaper would spin on the database.
	s := newSweep(func(int) (int, error) { return 1, nil })

	stop := start(t, s, reaper.Config{Interval: time.Hour, BatchSize: 10, MaxBackoff: time.Hour})
	defer stop()

	waitForSweep(t, s)

	select {
	case <-s.calls:
		t.Error("the reaper swept again without waiting out its interval")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRunKeepsGoingAfterAFailure(t *testing.T) {
	// A failed sweep is rolled back, so the reservations are still expired and
	// still there. Giving up would strand every hold behind the one that failed,
	// and a process that exited would be restarted into the same failure.
	s := newSweep(func(call int) (int, error) {
		if call == 1 {
			return 0, errors.New("database is unreachable")
		}

		return 0, nil
	})

	stop := start(t, s, reaper.Config{
		Interval:   10 * time.Millisecond,
		BatchSize:  10,
		MaxBackoff: time.Second,
	})
	defer stop()

	waitForSweep(t, s)
	waitForSweep(t, s)
}

func TestNewRejectsAConfigThatCannotWork(t *testing.T) {
	ok := func(int) (int, error) { return 0, nil }

	tests := []struct {
		name string
		cfg  reaper.Config
	}{
		{name: "no interval", cfg: reaper.Config{BatchSize: 10, MaxBackoff: time.Minute}},
		{name: "no batch", cfg: reaper.Config{Interval: time.Second, MaxBackoff: time.Minute}},
		// A ceiling below the floor would make the backoff shrink the wait
		// instead of growing it.
		{name: "backoff under the interval", cfg: reaper.Config{
			Interval:   time.Minute,
			BatchSize:  10,
			MaxBackoff: time.Second,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := reaper.New(newSweep(ok), tt.cfg, reaper.WithRegisterer(prometheus.NewRegistry()))
			if err == nil {
				t.Fatal("New() error = nil, want a configuration error")
			}
		})
	}

	t.Run("no sweep to drive", func(t *testing.T) {
		cfg := reaper.Config{Interval: time.Second, BatchSize: 10, MaxBackoff: time.Minute}

		if _, err := reaper.New(nil, cfg, reaper.WithRegisterer(prometheus.NewRegistry())); err == nil {
			t.Fatal("New() error = nil, want a configuration error")
		}
	})
}
