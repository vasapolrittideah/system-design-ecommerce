package kafkax

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	kafka "github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/outbox"
)

// dlqSuffix is what every dead-letter topic in this system is named after the
// one it failed out of.
const dlqSuffix = ".dlq"

// HeaderErrorReason is the header a message carries onto its DLQ, naming why
// the consumer gave up on it so a human reading the topic does not have to
// replay the message to find out.
const HeaderErrorReason = "error_reason"

// Message is one delivery: the routing Kafka carries beside the bytes.
// Decoding the payload is left to Handler, the same way Publisher never opens
// one — this package only ever moves bytes.
type Message struct {
	Topic   string
	Key     []byte
	Value   []byte
	Headers map[string]string
}

// Handler processes one message. An error routes it through retry and, once
// ConsumerConfig.MaxAttempts is spent, to the topic's own dead letter.
//
// A handler that wants to skip a message rather than retry it — an event type
// this build does not know about, forwards-compatibly — returns nil. Only a
// failure worth retrying should come back as an error.
type Handler func(ctx context.Context, msg Message) error

// ConsumerConfig is the environment-driven consumer configuration,
// conventionally loaded under a "KAFKA_CONSUMER_" prefix.
type ConsumerConfig struct {
	GroupID string `env:"GROUP_ID,required"`
	Topic   string `env:"TOPIC,required"`

	// MaxAttempts bounds how many times Handler runs for one delivery before
	// it is given up on and routed to the topic's .dlq instead. Finite so a
	// poison message costs a few attempts rather than an unbounded hold on
	// the partition it would otherwise take were it retried forever.
	MaxAttempts int `env:"MAX_ATTEMPTS" envDefault:"3"`

	// RetryBackoff is the pause between attempts. Small and fixed rather than
	// the outbox relay's doubling backoff: it exists to let a
	// millisecond-scale hiccup clear, not to wait out an outage, which the
	// broker redelivering after a restart already covers.
	RetryBackoff time.Duration `env:"RETRY_BACKOFF" envDefault:"200ms"`
}

// ConsumerOption customizes a consumer beyond what the environment expresses.
type ConsumerOption func(*consumerOptions)

type consumerOptions struct {
	registerer prometheus.Registerer
}

// WithRegisterer registers the consumer's metrics somewhere other than the
// default Prometheus registry — the registry pkg/observability owns, in a
// service, and a throwaway one in tests, which would otherwise panic on a
// second consumer registering the same collectors.
func WithRegisterer(reg prometheus.Registerer) ConsumerOption {
	return func(o *consumerOptions) {
		o.registerer = reg
	}
}

// Consumer reads one topic under one consumer group, committing an offset
// only once Handler has either succeeded or the message has been routed to
// the DLQ — so a redelivery after a crash always finds unfinished work rather
// than silently skipping it.
type Consumer struct {
	reader  *kafka.Reader
	dlq     *Publisher
	metrics *consumerMetrics
	cfg     ConsumerConfig
}

// NewConsumer builds a consumer over brokers, reading cfg.Topic under
// cfg.GroupID. dlq is what a message is handed to once retries are spent —
// typically the same Publisher a service's own outbox relay uses, since a
// dead letter is a message like any other and needs the same delivery
// guarantee.
func NewConsumer(brokers []string, cfg ConsumerConfig, dlq *Publisher, opts ...ConsumerOption) (*Consumer, error) {
	switch {
	case len(brokers) == 0:
		return nil, errors.New("kafkax: consumer needs at least one broker")
	case cfg.GroupID == "":
		return nil, errors.New("kafkax: consumer needs a group id")
	case cfg.Topic == "":
		return nil, errors.New("kafkax: consumer needs a topic")
	case dlq == nil:
		return nil, errors.New("kafkax: consumer needs a publisher for its dead letters")
	case cfg.MaxAttempts < 1:
		return nil, fmt.Errorf("kafkax: MaxAttempts is %d, want at least 1", cfg.MaxAttempts)
	}

	o := consumerOptions{registerer: prometheus.DefaultRegisterer}
	for _, opt := range opts {
		opt(&o)
	}

	metrics, err := newConsumerMetrics(o.registerer)
	if err != nil {
		return nil, err
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		GroupID: cfg.GroupID,
		Topic:   cfg.Topic,
	})

	return &Consumer{reader: reader, dlq: dlq, metrics: metrics, cfg: cfg}, nil
}

// Run fetches, hands each message to handle, and commits its offset until ctx
// is cancelled, then returns nil.
func (c *Consumer) Run(ctx context.Context, handle Handler) error {
	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			//nolint:nilerr // discarding err here is the point: shutdown is not a failure
			if ctx.Err() != nil {
				return nil
			}

			return fmt.Errorf("kafkax: fetch message: %w", err)
		}

		if err := c.process(ctx, msg, handle); err != nil {
			return err
		}
	}
}

// process runs handle up to MaxAttempts times, routes the message to its DLQ
// if every attempt failed, and commits the offset either way — a message on
// the DLQ is as handled as one handle accepted.
func (c *Consumer) process(ctx context.Context, msg kafka.Message, handle Handler) error {
	log := logger.From(ctx)
	m := toMessage(msg)

	var err error
	for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
		if err = handle(ctx, m); err == nil {
			break
		}

		log.Warn("kafkax: handler failed",
			zap.String("topic", msg.Topic),
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", c.cfg.MaxAttempts),
			zap.Error(err),
		)

		if attempt < c.cfg.MaxAttempts && !sleep(ctx, c.cfg.RetryBackoff) {
			// Shutting down. The offset stays uncommitted, so the next process
			// to own this partition redelivers the message rather than losing
			// it to a DLQ decision made under cancellation.
			//nolint:nilerr // discarding err here is the point: shutdown is not a failure
			return nil
		}
	}

	if err != nil {
		if dlqErr := c.publishDLQ(ctx, m, err); dlqErr != nil {
			return fmt.Errorf("kafkax: route message to dead letter: %w", dlqErr)
		}
		c.metrics.dlq.Inc()
	} else {
		c.metrics.processed.Inc()
	}

	if err := c.reader.CommitMessages(ctx, msg); err != nil {
		if ctx.Err() != nil {
			// Shutting down between the handler returning and the commit
			// landing is not a failure: the offset stays uncommitted, so the
			// next process to own this partition redelivers the message —
			// which handle must already tolerate, being a Kafka consumer.
			//nolint:nilerr // discarding err here is the point: shutdown is not a failure
			return nil
		}

		return fmt.Errorf("kafkax: commit offset: %w", err)
	}

	return nil
}

// publishDLQ hands msg to <topic>.dlq, carrying its original headers plus why
// this consumer gave up on it.
func (c *Consumer) publishDLQ(ctx context.Context, msg Message, cause error) error {
	headers := make(map[string]string, len(msg.Headers)+1)
	for k, v := range msg.Headers {
		headers[k] = v
	}
	headers[HeaderErrorReason] = cause.Error()

	return c.dlq.Publish(ctx, []outbox.Message{{
		AggregateID: string(msg.Key),
		Topic:       msg.Topic + dlqSuffix,
		Payload:     msg.Value,
		Headers:     headers,
	}})
}

// Close releases the reader. The Publisher passed to NewConsumer is the
// caller's own to close, since it may be shared with something else — the
// outbox relay's, for instance.
func (c *Consumer) Close() error {
	if err := c.reader.Close(); err != nil {
		return fmt.Errorf("kafkax: close reader: %w", err)
	}

	return nil
}

func toMessage(msg kafka.Message) Message {
	headers := make(map[string]string, len(msg.Headers))
	for _, h := range msg.Headers {
		headers[h.Key] = string(h.Value)
	}

	return Message{Topic: msg.Topic, Key: msg.Key, Value: msg.Value, Headers: headers}
}

// sleep waits for d, reporting false if ctx was cancelled first.
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
