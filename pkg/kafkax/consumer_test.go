package kafkax_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	kafka "github.com/segmentio/kafka-go"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/kafkax"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/outbox"
)

func consumerConfig(topic string) kafkax.ConsumerConfig {
	return kafkax.ConsumerConfig{
		GroupID:      "kafkax-consumer-test-" + topic,
		Topic:        topic,
		MaxAttempts:  3,
		RetryBackoff: 10 * time.Millisecond,
	}
}

func consumer(t *testing.T, addrs []string, cfg kafkax.ConsumerConfig, dlq *kafkax.Publisher) *kafkax.Consumer {
	t.Helper()

	// A registry of its own, or every test after the first fails to register
	// the same collector names against the global default registry.
	c, err := kafkax.NewConsumer(addrs, cfg, dlq, kafkax.WithRegisterer(prometheus.NewRegistry()))
	if err != nil {
		t.Fatalf("NewConsumer() error = %v, want nil", err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	return c
}

// runInBackground starts a consumer's Run loop and stops it — and waits for
// it to return — on cleanup, so a test never leaves a goroutine racing the
// broker container's teardown.
func runInBackground(t *testing.T, c *kafkax.Consumer, handle kafkax.Handler) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, handle) }()

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run() error = %v, want nil", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("Run() did not return after ctx was cancelled")
		}
	})
}

func TestConsumerHandlesEveryPublishedMessage(t *testing.T) {
	addrs, name := topic(t)
	pub := publisher(t, addrs)
	c := consumer(t, addrs, consumerConfig(name), pub)

	var got atomic.Int32
	handled := make(chan struct{}, 3)
	runInBackground(t, c, func(_ context.Context, msg kafkax.Message) error {
		if msg.Topic != name {
			t.Errorf("handler saw topic %q, want %q", msg.Topic, name)
		}
		got.Add(1)
		handled <- struct{}{}

		return nil
	})

	if err := pub.Publish(context.Background(), []outbox.Message{
		message(1, name, "aggregate-1"),
		message(2, name, "aggregate-2"),
		message(3, name, "aggregate-3"),
	}); err != nil {
		t.Fatalf("Publish() error = %v, want nil", err)
	}

	waitFor(t, handled, 3)

	if got.Load() != 3 {
		t.Errorf("handled %d message(s), want 3", got.Load())
	}
}

// A message a handler never accepts must still end up somewhere a human can
// find it, with the header naming why the consumer gave up.
func TestConsumerRoutesAPermanentFailureToTheDLQ(t *testing.T) {
	addrs, name := topic(t)
	// Declared up front: AllowAutoTopicCreation is off, the same as in
	// production, so publishing a dead letter to an undeclared topic would
	// fail for a reason unrelated to what this test checks.
	createTopic(t, addrs, name+".dlq")
	pub := publisher(t, addrs)
	cfg := consumerConfig(name)
	cfg.MaxAttempts = 2
	cfg.RetryBackoff = 5 * time.Millisecond
	c := consumer(t, addrs, cfg, pub)

	var attempts atomic.Int32
	runInBackground(t, c, func(_ context.Context, _ kafkax.Message) error {
		attempts.Add(1)

		return errors.New("boom")
	})

	want := message(1, name, "aggregate-1")
	if err := pub.Publish(context.Background(), []outbox.Message{want}); err != nil {
		t.Fatalf("Publish() error = %v, want nil", err)
	}

	dead := read(t, addrs, name+".dlq", 1)[0]

	if string(dead.Key) != want.AggregateID {
		t.Errorf("dlq key = %q, want %q", dead.Key, want.AggregateID)
	}
	if !bytes.Equal(dead.Value, want.Payload) {
		t.Errorf("dlq value = %v, want %v", dead.Value, want.Payload)
	}

	headers := map[string]string{}
	for _, h := range dead.Headers {
		headers[h.Key] = string(h.Value)
	}
	if headers[kafkax.HeaderErrorReason] != "boom" {
		t.Errorf("dlq %s = %q, want %q", kafkax.HeaderErrorReason, headers[kafkax.HeaderErrorReason], "boom")
	}

	if got := attempts.Load(); got != int32(cfg.MaxAttempts) {
		t.Errorf("handler ran %d time(s), want MaxAttempts (%d)", got, cfg.MaxAttempts)
	}
}

// A handler failing twice and succeeding on the third try must not be routed
// to the DLQ at all — retrying is the point.
func TestConsumerRetriesBeforeGivingUp(t *testing.T) {
	addrs, name := topic(t)
	pub := publisher(t, addrs)
	cfg := consumerConfig(name)
	cfg.RetryBackoff = 5 * time.Millisecond
	c := consumer(t, addrs, cfg, pub)

	var attempts atomic.Int32
	handled := make(chan struct{}, 1)
	runInBackground(t, c, func(_ context.Context, _ kafkax.Message) error {
		if attempts.Add(1) < 3 {
			return errors.New("transient")
		}
		handled <- struct{}{}

		return nil
	})

	if err := pub.Publish(context.Background(), []outbox.Message{message(1, name, "aggregate-1")}); err != nil {
		t.Fatalf("Publish() error = %v, want nil", err)
	}

	waitFor(t, handled, 1)

	if got := attempts.Load(); got != 3 {
		t.Errorf("handler ran %d time(s), want 3", got)
	}
}

func TestConsumerCommitsOnlyAfterHandling(t *testing.T) {
	addrs, name := topic(t)
	pub := publisher(t, addrs)
	cfg := consumerConfig(name)
	c := consumer(t, addrs, cfg, pub)

	var handledOnce sync.Once
	handled := make(chan struct{}, 1)
	runInBackground(t, c, func(_ context.Context, _ kafkax.Message) error {
		handledOnce.Do(func() { close(handled) })

		return nil
	})

	if err := pub.Publish(context.Background(), []outbox.Message{message(1, name, "aggregate-1")}); err != nil {
		t.Fatalf("Publish() error = %v, want nil", err)
	}

	select {
	case <-handled:
	case <-time.After(30 * time.Second):
		t.Fatal("message was never handled")
	}

	// A second consumer joining the same group after the first commits its
	// offset must see nothing left to read.
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: addrs,
		Topic:   name,
		GroupID: cfg.GroupID,
	})
	defer reader.Close() //nolint:errcheck // test cleanup

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := reader.ReadMessage(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReadMessage() error = %v, want a timeout — the offset should already be committed", err)
	}
}

func TestNewConsumerRejectsUnusableConfig(t *testing.T) {
	brokers := []string{"kafka:9092"}
	dlq, err := kafkax.NewPublisher(kafkax.PublisherConfig{
		Brokers: brokers, WriteTimeout: time.Second, MaxAttempts: 1,
	})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v, want nil", err)
	}
	defer dlq.Close() //nolint:errcheck // never dialed

	tests := []struct {
		name    string
		brokers []string
		cfg     kafkax.ConsumerConfig
		dlq     *kafkax.Publisher
	}{
		{"no brokers", nil, kafkax.ConsumerConfig{GroupID: "g", Topic: "t", MaxAttempts: 1}, dlq},
		{"no group id", brokers, kafkax.ConsumerConfig{Topic: "t", MaxAttempts: 1}, dlq},
		{"no topic", brokers, kafkax.ConsumerConfig{GroupID: "g", MaxAttempts: 1}, dlq},
		{"no dlq publisher", brokers, kafkax.ConsumerConfig{GroupID: "g", Topic: "t", MaxAttempts: 1}, nil},
		{"no max attempts", brokers, kafkax.ConsumerConfig{GroupID: "g", Topic: "t"}, dlq},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := kafkax.NewConsumer(tt.brokers, tt.cfg, tt.dlq)
			if err == nil {
				t.Fatal("NewConsumer() error = nil, want a rejection")
			}
			if c != nil {
				t.Error("NewConsumer() returned a consumer alongside an error")
			}
		})
	}
}

// waitFor blocks until want values have arrived on ch, failing rather than
// hanging forever if the consumer never delivers them.
func waitFor(t *testing.T, ch <-chan struct{}, want int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for range want {
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatalf("timed out waiting for the handler to run %d time(s)", want)
		}
	}
}
