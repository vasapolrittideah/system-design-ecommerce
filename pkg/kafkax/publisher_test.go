package kafkax_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kafka "github.com/segmentio/kafka-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/kafkax"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/outbox"
)

// Publisher is only ever reached through the interface the relay holds.
var _ outbox.Publisher = (*kafkax.Publisher)(nil)

// partitions is what a topic here is created with, matching the 6-12 the
// topic policy asks for. More than one is the point: a test against a
// single-partition topic cannot tell a working key from an ignored one.
const partitions = 6

// shared starts one KRaft broker the first time any test asks for it, and hands
// every later caller the same one. Each test takes a topic of its own instead
// of a cluster of its own, which keeps the cost to the seconds the container
// needs to come up rather than to that times the number of tests.
//
// The container is not stopped explicitly — testcontainers' reaper removes it
// when the test process exits, including when a test panics.
var shared = sync.OnceValues(start)

var topicSeq atomic.Uint64

func start() ([]string, error) {
	ctx := context.Background()

	cluster, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1")
	if err != nil {
		return nil, fmt.Errorf("start kafka: %w", err)
	}

	addrs, err := cluster.Brokers(ctx)
	if err != nil {
		return nil, fmt.Errorf("read broker addresses: %w", err)
	}

	return addrs, nil
}

// topic creates a topic of its own for t and returns its name.
func topic(t *testing.T) ([]string, string) {
	t.Helper()

	addrs, err := shared()
	if err != nil {
		t.Fatalf("kafka: %v", err)
	}

	name := fmt.Sprintf("ecommerce.test.%s.v%d",
		strings.ToLower(strings.ReplaceAll(t.Name(), "/", ".")), topicSeq.Add(1))

	createTopic(t, addrs, name)

	return addrs, name
}

// createTopic declares name on the shared broker, the same way
// deploy/k8s/infra/kafka's Job does — AllowAutoTopicCreation is off in
// production, so a test publishing to a topic it never declared, DLQ suffix
// included, would otherwise fail for a reason that has nothing to do with
// what it is testing.
func createTopic(t *testing.T, addrs []string, name string) {
	t.Helper()

	client := &kafka.Client{Addr: kafka.TCP(addrs...), Timeout: 30 * time.Second}
	res, err := client.CreateTopics(context.Background(), &kafka.CreateTopicsRequest{
		Topics: []kafka.TopicConfig{{
			Topic:             name,
			NumPartitions:     partitions,
			ReplicationFactor: 1,
		}},
	})
	if err != nil {
		t.Fatalf("create topic %s: %v", name, err)
	}
	if err := res.Errors[name]; err != nil {
		t.Fatalf("create topic %s: %v", name, err)
	}
}

func publisher(t *testing.T, addrs []string) *kafkax.Publisher {
	t.Helper()

	pub, err := kafkax.NewPublisher(kafkax.PublisherConfig{
		Brokers:      addrs,
		WriteTimeout: 10 * time.Second,
		MaxAttempts:  3,
	})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v, want nil", err)
	}
	t.Cleanup(func() {
		if err := pub.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	return pub
}

func message(id int64, name, aggregateID string) outbox.Message {
	return outbox.Message{
		ID:            id,
		AggregateType: "user",
		AggregateID:   aggregateID,
		EventType:     "UserRegistered",
		Topic:         name,
		Payload:       []byte{0x0a, 0x02, 0x68, 0x69},
		Headers: map[string]string{
			"correlation_id": "corr-1",
			"traceparent":    "00-trace-span-01",
		},
		CreatedAt: time.Now(),
	}
}

// read takes want messages off every partition of the topic, failing rather
// than blocking forever when the publisher wrote fewer than that.
func read(t *testing.T, addrs []string, name string, want int) []kafka.Message {
	t.Helper()

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: addrs,
		Topic:   name,
		// A group rather than a fixed partition, because which partition a key
		// lands on is what the publisher decides and the test must not assume.
		GroupID:     "kafkax-test",
		StartOffset: kafka.FirstOffset,
		MaxWait:     250 * time.Millisecond,
	})
	defer reader.Close() //nolint:errcheck // the test is done with it either way

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	out := make([]kafka.Message, 0, want)
	for range want {
		m, err := reader.ReadMessage(ctx)
		if err != nil {
			t.Fatalf("read message %d of %d: %v", len(out)+1, want, err)
		}
		out = append(out, m)
	}

	return out
}

func TestPublishDeliversEveryPartOfTheMessage(t *testing.T) {
	addrs, name := topic(t)
	pub := publisher(t, addrs)
	want := message(1, name, "01234567-89ab-cdef-0123-456789abcdef")

	if err := pub.Publish(context.Background(), []outbox.Message{want}); err != nil {
		t.Fatalf("Publish() error = %v, want nil", err)
	}

	got := read(t, addrs, name, 1)[0]

	if string(got.Key) != want.AggregateID {
		t.Errorf("key = %q, want the aggregate id %q", got.Key, want.AggregateID)
	}
	if !bytes.Equal(got.Value, want.Payload) {
		t.Errorf("value = %v, want %v", got.Value, want.Payload)
	}

	headers := map[string]string{}
	for _, h := range got.Headers {
		headers[h.Key] = string(h.Value)
	}
	// Without these the trace stops at the producer and the consumer's work
	// appears as an unrelated operation.
	for _, key := range []string{"correlation_id", "traceparent"} {
		if headers[key] != want.Headers[key] {
			t.Errorf("header %s = %q, want %q", key, headers[key], want.Headers[key])
		}
	}
}

func TestPublishKeepsOneAggregatesEventsOnOnePartition(t *testing.T) {
	addrs, name := topic(t)
	pub := publisher(t, addrs)

	const aggregateID = "11111111-1111-1111-1111-111111111111"
	batch := []outbox.Message{
		message(1, name, aggregateID),
		message(2, name, aggregateID),
		message(3, name, aggregateID),
	}
	batch[0].Payload = []byte("first")
	batch[1].Payload = []byte("second")
	batch[2].Payload = []byte("third")

	if err := pub.Publish(context.Background(), batch); err != nil {
		t.Fatalf("Publish() error = %v, want nil", err)
	}

	// One key means one partition, and one partition is where ordering lives.
	// Spread across six, these three could come back in any order at all.
	got := read(t, addrs, name, 3)
	for i, want := range []string{"first", "second", "third"} {
		if string(got[i].Value) != want {
			t.Errorf("message %d = %q, want %q", i, got[i].Value, want)
		}
		if got[i].Partition != got[0].Partition {
			t.Errorf("message %d landed on partition %d, want %d", i, got[i].Partition, got[0].Partition)
		}
	}
}

func TestPublishSpreadsDifferentAggregates(t *testing.T) {
	addrs, name := topic(t)
	pub := publisher(t, addrs)

	// Enough keys that landing them all on one partition would be a balancer
	// ignoring the key rather than luck.
	const keys = 40

	batch := make([]outbox.Message, 0, keys)
	for i := range keys {
		batch = append(batch, message(int64(i+1), name, fmt.Sprintf("aggregate-%02d", i)))
	}

	if err := pub.Publish(context.Background(), batch); err != nil {
		t.Fatalf("Publish() error = %v, want nil", err)
	}

	used := map[int]bool{}
	for _, m := range read(t, addrs, name, keys) {
		used[m.Partition] = true
	}
	if len(used) < 2 {
		t.Errorf("every message landed on %d partition(s), want the key to spread them", len(used))
	}
}

func TestPublishRefusesAnUndeclaredTopic(t *testing.T) {
	addrs, _ := topic(t)
	pub := publisher(t, addrs)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A topic conjured by a typo is one partition forever, so the write has to
	// fail instead.
	err := pub.Publish(ctx, []outbox.Message{message(1, "ecommerce.nobody.declared.this.v1", "aggregate-1")})
	if err == nil {
		t.Fatal("Publish() error = nil, want a rejection for an undeclared topic")
	}
}

func TestPublishWithoutMessagesIsANoOp(t *testing.T) {
	addrs, _ := topic(t)

	if err := publisher(t, addrs).Publish(context.Background(), nil); err != nil {
		t.Errorf("Publish() error = %v, want nil", err)
	}
}

func TestNewPublisherRejectsUnusableConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  kafkax.PublisherConfig
	}{
		{"no brokers", kafkax.PublisherConfig{WriteTimeout: time.Second, MaxAttempts: 3}},
		{"no attempts", kafkax.PublisherConfig{Brokers: []string{"kafka:9092"}, WriteTimeout: time.Second}},
		{"no write timeout", kafkax.PublisherConfig{Brokers: []string{"kafka:9092"}, MaxAttempts: 3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub, err := kafkax.NewPublisher(tt.cfg)
			if err == nil {
				t.Fatal("NewPublisher() error = nil, want a rejection")
			}
			if pub != nil {
				t.Error("NewPublisher() returned a publisher alongside an error")
			}
		})
	}
}

// A publish that fails must say so, because the relay marks its rows published
// on a nil error and there is no second chance to notice.
func TestPublishReportsAnUnreachableBroker(t *testing.T) {
	pub, err := kafkax.NewPublisher(kafkax.PublisherConfig{
		Brokers:      []string{"127.0.0.1:1"},
		WriteTimeout: time.Second,
		MaxAttempts:  1,
	})
	if err != nil {
		t.Fatalf("NewPublisher() error = %v, want nil", err)
	}
	defer pub.Close() //nolint:errcheck // closing a writer that never connected

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err = pub.Publish(ctx, []outbox.Message{message(1, "ecommerce.unreachable.v1", "aggregate-1")})
	if err == nil {
		t.Fatal("Publish() error = nil, want a failure")
	}
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
		t.Fatal("Publish() hung until the test's own deadline rather than failing")
	}
}
