package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
)

// Message is an outbox row on its way to the broker.
type Message struct {
	// ID is the outbox row's sequence number. It is also the publishing order:
	// consumers rely on OrderPaid never arriving before OrderCreated.
	ID int64

	AggregateType string

	// AggregateID is the message key. Keying by aggregate is what puts every
	// event for one order on one partition, which is where ordering is kept.
	AggregateID string

	EventType string
	Topic     string
	Payload   []byte
	Headers   map[string]string
	CreatedAt time.Time
}

// Publisher hands messages to the broker.
//
// It is an interface so this package never imports a Kafka client: the relay's
// job is draining a table in order and exactly the transactional dance around
// it, none of which has anything to do with the transport. Publish must return
// an error unless every message reached the broker — the relay marks the whole
// batch published on nil.
type Publisher interface {
	Publish(ctx context.Context, messages []Message) error
}

// RelayConfig is the environment-driven relay configuration, conventionally
// loaded under an "OUTBOX_" prefix.
type RelayConfig struct {
	// BatchSize caps how many rows one cycle claims. The batch is held in a
	// transaction for as long as publishing takes, so a large batch trades
	// throughput for a longer lock and a longer replay after a crash.
	BatchSize int `env:"BATCH_SIZE" envDefault:"100"`

	// PollInterval is how long the relay waits after finding nothing to do. It
	// is the floor on how stale an event can be, so it is also the reason the
	// entry call returns before the workflow finishes rather than after.
	PollInterval time.Duration `env:"POLL_INTERVAL" envDefault:"1s"`

	// MaxBackoff caps the wait between retries after a failed cycle. A broker
	// outage must not turn into a poll storm, and it must not turn into a relay
	// that has backed off to twenty minutes when the broker comes back either.
	MaxBackoff time.Duration `env:"MAX_BACKOFF" envDefault:"30s"`

	// StatsInterval is how often the backlog is measured. Counting unpublished
	// rows walks the partial index, which is cheap while the relay is keeping
	// up and progressively less so when it is not — exactly when the loop can
	// least afford to do it every cycle. Measuring on its own cadence, near the
	// scrape interval, keeps the cost flat.
	StatsInterval time.Duration `env:"STATS_INTERVAL" envDefault:"15s"`
}

// RelayOption customizes a relay beyond what the environment expresses.
type RelayOption func(*relayOptions)

type relayOptions struct {
	registerer prometheus.Registerer
}

// WithRegisterer registers the relay metrics somewhere other than the default
// Prometheus registry — the registry pkg/observability owns, in a service, and
// a throwaway one in tests, which would otherwise panic on the second relay
// registering the same collectors.
func WithRegisterer(reg prometheus.Registerer) RelayOption {
	return func(o *relayOptions) {
		o.registerer = reg
	}
}

// Relay publishes outbox rows and marks them published.
//
// One instance per service is the intended deployment. SKIP LOCKED is not there
// to scale the relay out: it is there so the old and new pods overlapping
// during a rolling restart cannot publish the same row twice. Running several
// permanently would let one instance publish an aggregate's later event while
// another is still holding the earlier one, which is precisely the ordering the
// message key exists to preserve.
type Relay struct {
	pool      *pgxpool.Pool
	publisher Publisher
	tx        *txmanager.Manager
	metrics   *metrics
	cfg       RelayConfig
}

// NewRelay builds a relay over pool.
func NewRelay(pool *pgxpool.Pool, publisher Publisher, cfg RelayConfig, opts ...RelayOption) (*Relay, error) {
	switch {
	case publisher == nil:
		return nil, errors.New("outbox: relay needs a publisher")
	case cfg.BatchSize < 1:
		return nil, fmt.Errorf("outbox: BatchSize is %d, want at least 1", cfg.BatchSize)
	case cfg.PollInterval <= 0:
		return nil, fmt.Errorf("outbox: PollInterval is %v, want a positive duration", cfg.PollInterval)
	case cfg.MaxBackoff < cfg.PollInterval:
		return nil, fmt.Errorf("outbox: MaxBackoff is %v, want at least PollInterval (%v)", cfg.MaxBackoff, cfg.PollInterval)
	case cfg.StatsInterval <= 0:
		return nil, fmt.Errorf("outbox: StatsInterval is %v, want a positive duration", cfg.StatsInterval)
	}

	o := relayOptions{registerer: prometheus.DefaultRegisterer}
	for _, opt := range opts {
		opt(&o)
	}

	metrics, err := newMetrics(o.registerer)
	if err != nil {
		return nil, err
	}

	return &Relay{
		pool:      pool,
		publisher: publisher,
		tx:        txmanager.New(pool),
		metrics:   metrics,
		cfg:       cfg,
	}, nil
}

// Run drains the outbox until ctx is cancelled, then returns nil.
//
// A cycle that fills its batch is followed immediately by another, so a backlog
// is worked off at the speed of the broker rather than of the poll interval. A
// cycle that fails is retried with a doubling wait: the row is still there and
// still unpublished, so the only thing a failure costs is time.
//
// Errors are logged rather than returned because there is nobody to return them
// to — the relay is the thing that makes a committed event eventually reach the
// broker, and giving up would silently strand every event behind it. What
// notices a relay that is failing forever is the alert on outbox backlog.
func (r *Relay) Run(ctx context.Context) error {
	log := logger.From(ctx)
	backoff := r.cfg.PollInterval

	// Zero forces the first measurement before the first publish, so a relay
	// that comes up to an existing backlog reports it immediately.
	var lastStats time.Time

	for {
		if time.Since(lastStats) >= r.cfg.StatsInterval {
			if err := r.observeBacklog(ctx); err != nil && ctx.Err() == nil {
				log.Warn("outbox: backlog measurement failed", zap.Error(err))
			}
			lastStats = time.Now()
		}

		published, err := r.publishBatch(ctx)

		var wait time.Duration
		switch {
		case ctx.Err() != nil:
			// The cycle was cut short by shutdown, not by a real failure. Its
			// transaction rolled back, so the rows are still waiting.
			return nil
		case err != nil:
			wait = backoff
			backoff = min(backoff*2, r.cfg.MaxBackoff)
			log.Warn("outbox: publish cycle failed",
				zap.Error(err),
				zap.Duration("retry_in", wait),
			)
		default:
			backoff = r.cfg.PollInterval
			if published == r.cfg.BatchSize {
				// A full batch usually means there is more behind it.
				continue
			}
			wait = r.cfg.PollInterval
		}

		if !sleep(ctx, wait) {
			return nil
		}
	}
}

// publishBatch claims a batch, publishes it, and marks it published — all in
// one transaction.
//
// Holding the transaction open across the publish is deliberate. The rows stay
// locked, so a second relay skips them; a publish that fails rolls the claim
// back and leaves them for the next cycle; and a relay that dies mid-publish
// releases its locks when the connection dies. The cost is that a slow broker
// holds a transaction open, which is why BatchSize is bounded.
//
// The one thing this cannot promise is that a message is published exactly
// once: a crash after the broker accepted the batch but before the commit
// replays it. That is why consumers are required to be idempotent.
func (r *Relay) publishBatch(ctx context.Context) (int, error) {
	var published int

	err := r.tx.Do(ctx, func(ctx context.Context) error {
		db := txmanager.From(ctx, r.pool)

		messages, err := claim(ctx, db, r.cfg.BatchSize)
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			return nil
		}

		if err := r.publisher.Publish(ctx, messages); err != nil {
			return fmt.Errorf("outbox: publish %d message(s): %w", len(messages), err)
		}

		if err := markPublished(ctx, db, messages); err != nil {
			return err
		}
		published = len(messages)

		return nil
	})
	if err != nil {
		// A cycle cut short by shutdown is not a failure, and counting it as
		// one would put a spike in the error rate on every deploy.
		if ctx.Err() == nil {
			r.metrics.failures.Inc()
		}

		return 0, err
	}

	r.metrics.published.Add(float64(published))

	return published, nil
}

// observeBacklog measures the queue: how many rows are waiting and how long the
// oldest has been waiting.
//
// It reads outside any transaction, so it sees only committed rows — which is
// what the backlog means. The two numbers come from one statement because they
// have to describe the same instant to be comparable.
func (r *Relay) observeBacklog(ctx context.Context) error {
	const sql = `
		SELECT count(*), coalesce(extract(epoch FROM now() - min(created_at)), 0)::float8
		FROM outbox
		WHERE published_at IS NULL`

	var (
		backlog int64
		age     float64
	)
	if err := r.pool.QueryRow(ctx, sql).Scan(&backlog, &age); err != nil {
		return fmt.Errorf("outbox: measure backlog: %w", err)
	}

	r.metrics.backlog.Set(float64(backlog))
	r.metrics.oldestAge.Set(age)

	return nil
}

// claim locks the oldest unpublished rows for this transaction. SKIP LOCKED
// passes over rows another relay is already holding instead of queueing behind
// them, so an overlapping instance neither blocks nor duplicates.
func claim(ctx context.Context, db txmanager.DBTX, limit int) ([]Message, error) {
	const sql = `
		SELECT id, aggregate_type, aggregate_id, event_type, topic, payload, headers, created_at
		FROM outbox
		WHERE published_at IS NULL
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED`

	rows, err := db.Query(ctx, sql, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox: claim batch: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		err := rows.Scan(&m.ID, &m.AggregateType, &m.AggregateID, &m.EventType,
			&m.Topic, &m.Payload, &m.Headers, &m.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("outbox: scan claimed row: %w", err)
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox: read claimed rows: %w", err)
	}

	return messages, nil
}

func markPublished(ctx context.Context, db txmanager.DBTX, messages []Message) error {
	ids := make([]int64, len(messages))
	for i, m := range messages {
		ids[i] = m.ID
	}

	const sql = `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`

	tag, err := db.Exec(ctx, sql, ids)
	if err != nil {
		return fmt.Errorf("outbox: mark %d row(s) published: %w", len(ids), err)
	}
	if tag.RowsAffected() != int64(len(ids)) {
		// The rows are locked by this transaction, so nothing else can have
		// touched them. A mismatch means the statement did not do what the
		// batch says it did, and committing would drop the difference.
		return fmt.Errorf("outbox: marked %d row(s) published, want %d", tag.RowsAffected(), len(ids))
	}

	return nil
}

// sleep waits for d, reporting false if ctx was cancelled first. A zero wait
// still checks for cancellation, so a relay that is keeping up with a backlog
// remains interruptible.
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
