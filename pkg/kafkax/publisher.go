// Package kafkax publishes what the outbox has collected.
//
// It exists as the one implementation of outbox.Publisher so that pkg/outbox
// never imports a broker client, and so that every service's relay agrees on
// the producer settings that decide whether an acknowledged write survives.
//
// A relay wires it and hands it to the relay loop:
//
//	pub, err := kafkax.NewPublisher(cfg)
//	defer pub.Close()
//	relay, err := outbox.NewRelay(pool, pub, relayCfg)
//
// Nothing here builds an envelope or reads a payload. A message arrives with
// its topic, its key, and its bytes already decided by the service that raised
// the event.
package kafkax

import (
	"context"
	"errors"
	"fmt"
	"time"

	kafka "github.com/segmentio/kafka-go"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/outbox"
)

// PublisherConfig is the environment-driven producer configuration,
// conventionally loaded under a "KAFKA_" prefix.
type PublisherConfig struct {
	// Brokers is the bootstrap list. Any reachable broker is enough — the
	// client discovers the rest and finds the leader for each partition.
	Brokers []string `env:"BROKERS,required" envSeparator:","`

	// WriteTimeout bounds one publish call, and with it how long the relay's
	// transaction stays open holding its claimed rows. A broker that has gone
	// away must surface as an error the relay can roll back and retry, rather
	// than as a cycle that never ends.
	WriteTimeout time.Duration `env:"WRITE_TIMEOUT" envDefault:"10s"`

	// MaxAttempts is how many times the client itself retries a batch before
	// giving up. It is deliberately small: the relay retries the whole cycle
	// with backoff, and the rows are still in the table until it succeeds, so
	// nothing is lost by failing early and everything is delayed by failing
	// slowly.
	MaxAttempts int `env:"MAX_ATTEMPTS" envDefault:"3"`
}

// Publisher writes outbox messages to Kafka.
type Publisher struct {
	writer *kafka.Writer
}

// NewPublisher builds a publisher over the configured brokers.
func NewPublisher(cfg PublisherConfig) (*Publisher, error) {
	if len(cfg.Brokers) == 0 {
		return nil, errors.New("kafkax: publisher needs at least one broker")
	}
	if cfg.MaxAttempts < 1 {
		return nil, fmt.Errorf("kafkax: MaxAttempts is %d, want at least 1", cfg.MaxAttempts)
	}
	if cfg.WriteTimeout <= 0 {
		return nil, fmt.Errorf("kafkax: WriteTimeout is %v, want a positive duration", cfg.WriteTimeout)
	}

	return &Publisher{
		writer: &kafka.Writer{
			Addr: kafka.TCP(cfg.Brokers...),

			// No Topic: the outbox row carries it, so the service that owns an
			// event decides where it goes without a topic map shared by
			// everyone.
			Balancer: &kafka.Murmur2Balancer{},

			// The whole guarantee. RequireAll waits for every in-sync replica,
			// so an acknowledged write survives the leader dying; anything less
			// acknowledges a message that a failover can still lose, and the
			// relay would have marked the row published.
			RequiredAcks: kafka.RequireAll,

			// Async would return nil before the broker has seen anything and
			// report failures to a callback nobody here reads. The relay marks
			// rows published on a nil error, so an async writer turns every
			// broker outage into silently dropped events.
			Async: false,

			MaxAttempts:  cfg.MaxAttempts,
			WriteTimeout: cfg.WriteTimeout,

			// Topics are declared, never conjured. A broker asked to create one
			// on first write makes it with num.partitions -- one, unless
			// somebody set otherwise -- and a topic's partition count cannot be
			// raised later without changing which partition a key lands on,
			// which is the ordering guarantee this system is built on. Publish
			// to a topic nobody declared and it fails, which is the right
			// answer for a typo as well.
			AllowAutoTopicCreation: false,
		},
	}, nil
}

// Publish writes every message or returns an error.
//
// The all-or-nothing contract is the relay's: it marks the batch published on a
// nil error, so a partial success reported as success would strand the rest of
// the rows as published-but-never-sent. A failure costs only time, because the
// transaction that claimed the rows rolls back and the next cycle finds them
// again — which is also why a redelivery is normal and why every consumer
// claims the event before handling it.
func (p *Publisher) Publish(ctx context.Context, messages []outbox.Message) error {
	if len(messages) == 0 {
		return nil
	}

	batch := make([]kafka.Message, len(messages))
	for i := range messages {
		batch[i] = kafka.Message{
			Topic: messages[i].Topic,

			// Keyed by aggregate, which is what puts one order's events on one
			// partition and is the only ordering this system relies on.
			Key:     []byte(messages[i].AggregateID),
			Value:   messages[i].Payload,
			Headers: headers(messages[i].Headers),
		}
	}

	if err := p.writer.WriteMessages(ctx, batch...); err != nil {
		return fmt.Errorf("kafkax: publish %d message(s): %w", len(batch), err)
	}

	return nil
}

// Close flushes and shuts the writer down. A relay calls it after its loop has
// returned, where there is nothing left in flight to lose.
func (p *Publisher) Close() error {
	if err := p.writer.Close(); err != nil {
		return fmt.Errorf("kafkax: close writer: %w", err)
	}

	return nil
}

// headers carries traceparent and correlation_id to the consumer, which is the
// whole reason a trace survives the hop. Kafka header values are bytes and the
// map is small, so there is nothing to encode.
func headers(h map[string]string) []kafka.Header {
	if len(h) == 0 {
		return nil
	}

	out := make([]kafka.Header, 0, len(h))
	for k, v := range h {
		out = append(out, kafka.Header{Key: k, Value: []byte(v)})
	}

	return out
}
