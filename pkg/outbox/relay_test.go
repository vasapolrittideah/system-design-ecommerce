package outbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func relayConfig() RelayConfig {
	return RelayConfig{
		BatchSize:     10,
		PollInterval:  10 * time.Millisecond,
		MaxBackoff:    50 * time.Millisecond,
		StatsInterval: 10 * time.Millisecond,
	}
}

// publisher records what it was handed and can be made to fail or to stall, so
// a test can hold a claim open while another relay tries to take the same rows.
type publisher struct {
	mu       sync.Mutex
	batches  [][]Message
	err      error
	entered  chan struct{} // signalled as Publish is entered, if non-nil
	release  chan struct{} // Publish waits for this to close, if non-nil
	released bool
}

func (p *publisher) Publish(ctx context.Context, messages []Message) error {
	if p.entered != nil {
		p.entered <- struct{}{}
	}
	if p.release != nil {
		<-p.release
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.batches = append(p.batches, messages)

	return p.err
}

func (p *publisher) published() []Message {
	p.mu.Lock()
	defer p.mu.Unlock()

	var all []Message
	for _, batch := range p.batches {
		all = append(all, batch...)
	}

	return all
}

func (p *publisher) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.batches)
}

func newRelay(t *testing.T, pool *pgxpool.Pool, pub Publisher, cfg RelayConfig) *Relay {
	t.Helper()

	// Its own registry per relay: the collectors are registered by name, so
	// sharing one would fail the second relay in a test that needs two.
	relay, err := NewRelay(pool, pub, cfg, WithRegisterer(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("NewRelay() error = %v, want nil", err)
	}

	return relay
}

// seed writes n records whose event types identify their position, so ordering
// assertions read as something other than row ids.
func seed(t *testing.T, ctx context.Context, pool *pgxpool.Pool, eventTypes ...string) {
	t.Helper()

	records := make([]Record, len(eventTypes))
	for i, eventType := range eventTypes {
		records[i] = record()
		records[i].EventType = eventType
	}

	if err := Write(ctx, pool, records...); err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}
}

func unpublished(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()

	var count int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&count)
	if err != nil {
		t.Fatalf("count unpublished: %v", err)
	}

	return count
}

// waitFor polls until cond holds, which is how a test observes a loop it does
// not step through itself.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", msg)
}

func TestRelayPublishesAndMarksRows(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	pub := &publisher{}
	relay := newRelay(t, pool, pub, relayConfig())

	want := record()
	if err := Write(ctx, pool, want); err != nil {
		t.Fatalf("Write() error = %v, want nil", err)
	}

	published, err := relay.publishBatch(ctx)
	if err != nil {
		t.Fatalf("publishBatch() error = %v, want nil", err)
	}
	if published != 1 {
		t.Errorf("publishBatch() = %d, want 1", published)
	}

	messages := pub.published()
	if len(messages) != 1 {
		t.Fatalf("publisher got %d messages, want 1", len(messages))
	}

	got := messages[0]
	if got.Topic != want.Topic {
		t.Errorf("Topic = %q, want %q", got.Topic, want.Topic)
	}
	// The key the broker partitions on has to survive the trip through the
	// table, or per-aggregate ordering is lost at the last hop.
	if got.AggregateID != want.AggregateID {
		t.Errorf("AggregateID = %q, want %q", got.AggregateID, want.AggregateID)
	}
	if got.EventType != want.EventType {
		t.Errorf("EventType = %q, want %q", got.EventType, want.EventType)
	}
	if string(got.Payload) != string(want.Payload) {
		t.Errorf("Payload = %v, want %v", got.Payload, want.Payload)
	}
	if got.Headers["traceparent"] != want.Headers["traceparent"] {
		t.Errorf("Headers = %v, want %v", got.Headers, want.Headers)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}

	if n := unpublished(t, ctx, pool); n != 0 {
		t.Errorf("%d rows still unpublished, want 0", n)
	}
}

func TestRelayPublishesInInsertionOrder(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	pub := &publisher{}
	relay := newRelay(t, pool, pub, relayConfig())

	want := []string{"OrderCreated", "OrderPaid", "OrderShipped"}
	seed(t, ctx, pool, want...)

	if _, err := relay.publishBatch(ctx); err != nil {
		t.Fatalf("publishBatch() error = %v, want nil", err)
	}

	messages := pub.published()
	if len(messages) != len(want) {
		t.Fatalf("publisher got %d messages, want %d", len(messages), len(want))
	}
	for i, m := range messages {
		if m.EventType != want[i] {
			t.Errorf("message %d = %q, want %q", i, m.EventType, want[i])
		}
	}
}

func TestRelayLeavesRowsWhenPublishingFails(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	wantErr := errors.New("broker unreachable")
	pub := &publisher{err: wantErr}
	relay := newRelay(t, pool, pub, relayConfig())

	seed(t, ctx, pool, "OrderCreated", "OrderPaid")

	published, err := relay.publishBatch(ctx)
	if !errors.Is(err, wantErr) {
		t.Fatalf("publishBatch() error = %v, want %v", err, wantErr)
	}
	if published != 0 {
		t.Errorf("publishBatch() = %d, want 0", published)
	}

	// The claim rolled back with the failure, so the rows are still queued and
	// the next cycle will try them again.
	if n := unpublished(t, ctx, pool); n != 2 {
		t.Errorf("%d rows unpublished, want 2", n)
	}
}

func TestRelayClaimsNoMoreThanBatchSize(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	pub := &publisher{}
	cfg := relayConfig()
	cfg.BatchSize = 2
	relay := newRelay(t, pool, pub, cfg)

	seed(t, ctx, pool, "First", "Second", "Third", "Fourth", "Fifth")

	published, err := relay.publishBatch(ctx)
	if err != nil {
		t.Fatalf("publishBatch() error = %v, want nil", err)
	}
	if published != 2 {
		t.Errorf("publishBatch() = %d, want 2", published)
	}
	if n := unpublished(t, ctx, pool); n != 3 {
		t.Errorf("%d rows unpublished, want 3", n)
	}
}

func TestRelayWithNothingToPublish(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	pub := &publisher{}
	relay := newRelay(t, pool, pub, relayConfig())

	published, err := relay.publishBatch(ctx)
	if err != nil {
		t.Fatalf("publishBatch() error = %v, want nil", err)
	}
	if published != 0 {
		t.Errorf("publishBatch() = %d, want 0", published)
	}
	// An empty cycle must not hand the broker an empty batch.
	if calls := pub.calls(); calls != 0 {
		t.Errorf("publisher called %d times, want 0", calls)
	}
}

func TestRelaySkipsRowsAnotherRelayHasClaimed(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)

	seed(t, ctx, pool, "OrderCreated")

	// The first relay stalls inside Publish while holding its claim, which is
	// what a rolling restart looks like from the second relay's side.
	held := &publisher{entered: make(chan struct{}), release: make(chan struct{})}
	first := newRelay(t, pool, held, relayConfig())

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := first.publishBatch(ctx); err != nil {
			t.Errorf("first publishBatch() error = %v, want nil", err)
		}
	}()

	<-held.entered

	idle := &publisher{}
	second := newRelay(t, pool, idle, relayConfig())

	published, err := second.publishBatch(ctx)
	if err != nil {
		t.Fatalf("second publishBatch() error = %v, want nil", err)
	}
	// SKIP LOCKED is the point: the second relay steps over the locked row
	// rather than blocking on it or publishing it twice.
	if published != 0 {
		t.Errorf("second publishBatch() = %d, want 0", published)
	}
	if calls := idle.calls(); calls != 0 {
		t.Errorf("second publisher called %d times, want 0", calls)
	}

	close(held.release)
	<-done

	if n := unpublished(t, ctx, pool); n != 0 {
		t.Errorf("%d rows unpublished after the first relay finished, want 0", n)
	}
}

func TestRelayCountsWhatItPublished(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	relay := newRelay(t, pool, &publisher{}, relayConfig())

	seed(t, ctx, pool, "OrderCreated", "OrderPaid")

	if _, err := relay.publishBatch(ctx); err != nil {
		t.Fatalf("publishBatch() error = %v, want nil", err)
	}

	if got := testutil.ToFloat64(relay.metrics.published); got != 2 {
		t.Errorf("outbox_published_total = %v, want 2", got)
	}
	if got := testutil.ToFloat64(relay.metrics.failures); got != 0 {
		t.Errorf("outbox_publish_failures_total = %v, want 0", got)
	}
}

func TestRelayCountsFailedCycles(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	relay := newRelay(t, pool, &publisher{err: errors.New("broker unreachable")}, relayConfig())

	seed(t, ctx, pool, "OrderCreated")

	if _, err := relay.publishBatch(ctx); err == nil {
		t.Fatal("publishBatch() error = nil, want error")
	}

	if got := testutil.ToFloat64(relay.metrics.failures); got != 1 {
		t.Errorf("outbox_publish_failures_total = %v, want 1", got)
	}
	// Nothing reached the broker, so nothing may be counted as published.
	if got := testutil.ToFloat64(relay.metrics.published); got != 0 {
		t.Errorf("outbox_published_total = %v, want 0", got)
	}
}

func TestRelayReportsTheBacklog(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	relay := newRelay(t, pool, &publisher{}, relayConfig())

	seed(t, ctx, pool, "First", "Second", "Third")

	if err := relay.observeBacklog(ctx); err != nil {
		t.Fatalf("observeBacklog() error = %v, want nil", err)
	}
	if got := testutil.ToFloat64(relay.metrics.backlog); got != 3 {
		t.Errorf("outbox_backlog_rows = %v, want 3", got)
	}
	// Depth without age cannot distinguish a burst from a stalled relay, so the
	// age has to be a real measurement rather than a placeholder.
	if got := testutil.ToFloat64(relay.metrics.oldestAge); got <= 0 {
		t.Errorf("outbox_oldest_unpublished_seconds = %v, want above 0", got)
	}

	if _, err := relay.publishBatch(ctx); err != nil {
		t.Fatalf("publishBatch() error = %v, want nil", err)
	}

	if err := relay.observeBacklog(ctx); err != nil {
		t.Fatalf("observeBacklog() error = %v, want nil", err)
	}
	if got := testutil.ToFloat64(relay.metrics.backlog); got != 0 {
		t.Errorf("outbox_backlog_rows = %v, want 0", got)
	}
	// An empty queue has no oldest row; reporting the last age forever would
	// leave the alert firing after the backlog cleared.
	if got := testutil.ToFloat64(relay.metrics.oldestAge); got != 0 {
		t.Errorf("outbox_oldest_unpublished_seconds = %v, want 0", got)
	}
}

func TestRunDrainsABacklog(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool := setup(t)
	pub := &publisher{}
	cfg := relayConfig()
	cfg.BatchSize = 2
	relay := newRelay(t, pool, pub, cfg)

	seed(t, ctx, pool, "First", "Second", "Third", "Fourth", "Fifth")

	stopped := make(chan error, 1)
	go func() { stopped <- relay.Run(ctx) }()

	waitFor(t, func() bool { return unpublished(t, context.Background(), pool) == 0 }, "the backlog to drain")

	if got := len(pub.published()); got != 5 {
		t.Errorf("publisher got %d messages, want 5", got)
	}

	// The loop measures on its own cadence, so the gauge catches up shortly
	// after the rows do.
	waitFor(t, func() bool { return testutil.ToFloat64(relay.metrics.backlog) == 0 }, "the backlog gauge to settle")

	cancel()
	if err := <-stopped; err != nil {
		t.Errorf("Run() error = %v, want nil", err)
	}
}

func TestRunKeepsGoingAfterAFailedCycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool := setup(t)
	pub := &publisher{err: errors.New("broker unreachable")}
	relay := newRelay(t, pool, pub, relayConfig())

	seed(t, ctx, pool, "OrderCreated")

	stopped := make(chan error, 1)
	go func() { stopped <- relay.Run(ctx) }()

	// A broker outage must not end the loop: the row is still queued, so the
	// relay has to come back for it.
	waitFor(t, func() bool { return pub.calls() >= 2 }, "the relay to retry")

	if n := unpublished(t, context.Background(), pool); n != 1 {
		t.Errorf("%d rows unpublished, want 1", n)
	}

	cancel()
	if err := <-stopped; err != nil {
		t.Errorf("Run() error = %v, want nil", err)
	}
}

func TestRunStopsWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pool := setup(t)
	relay := newRelay(t, pool, &publisher{}, relayConfig())

	stopped := make(chan error, 1)
	go func() { stopped <- relay.Run(ctx) }()

	cancel()

	select {
	case err := <-stopped:
		// Shutdown is not a failure; a relay that returned an error here would
		// fail every graceful termination.
		if err != nil {
			t.Errorf("Run() error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after the context was cancelled")
	}
}

func TestNewRelayRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		pub    Publisher
		mutate func(*RelayConfig)
	}{
		{"no publisher", nil, func(*RelayConfig) {}},
		{"zero batch size", &publisher{}, func(c *RelayConfig) { c.BatchSize = 0 }},
		{"zero poll interval", &publisher{}, func(c *RelayConfig) { c.PollInterval = 0 }},
		{"backoff below poll interval", &publisher{}, func(c *RelayConfig) { c.MaxBackoff = c.PollInterval / 2 }},
		{"zero stats interval", &publisher{}, func(c *RelayConfig) { c.StatsInterval = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := relayConfig()
			tt.mutate(&cfg)

			got, err := NewRelay(nil, tt.pub, cfg)
			if err == nil {
				t.Fatalf("NewRelay() = %+v, want error", got)
			}
		})
	}
}
