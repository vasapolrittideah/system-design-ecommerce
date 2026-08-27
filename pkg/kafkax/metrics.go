package kafkax

import (
	"errors"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// consumerLabels identify which subscription a sample came from. A process may
// run several consumers — order's worker runs two, one per topic it reads — and
// without these the counters below are one number covering all of them, which
// answers neither "which subscription is giving up on messages" nor "is this
// one still working".
var consumerLabels = []string{"group_id", "topic"}

// consumerMetrics is what makes a consumer giving up on messages visible. A
// message on a DLQ breaks nothing that anyone can see at the topic it left —
// the partition keeps draining, the group stays healthy — so this counter is
// the only place a growing DLQ shows up before somebody goes looking for it.
//
// Its fields are already bound to one consumer's labels, so Run increments them
// without knowing they carry any.
type consumerMetrics struct {
	processed prometheus.Counter
	dlq       prometheus.Counter
}

func newConsumerMetrics(reg prometheus.Registerer, cfg ConsumerConfig) (*consumerMetrics, error) {
	processed, err := counterVec(reg, prometheus.CounterOpts{
		Name: "kafka_consumer_messages_processed_total",
		Help: "Messages a consumer's handler accepted.",
	})
	if err != nil {
		return nil, err
	}

	dlq, err := counterVec(reg, prometheus.CounterOpts{
		Name: "kafka_consumer_dlq_messages_total",
		Help: "Messages routed to a topic's .dlq after exhausting every retry.",
	})
	if err != nil {
		return nil, err
	}

	return &consumerMetrics{
		processed: processed.WithLabelValues(cfg.GroupID, cfg.Topic),
		dlq:       dlq.WithLabelValues(cfg.GroupID, cfg.Topic),
	}, nil
}

// counterVec registers opts on reg, or returns the vector already registered
// under that name.
//
// The second case is the ordinary one rather than a fallback: a collector is
// registered per name, and consumers in one process share a registry, so every
// consumer after the first finds its vectors already there. Treating that as
// the error Register reports is what crash-looped order's worker — the process
// exited before its second subscription ever started.
func counterVec(reg prometheus.Registerer, opts prometheus.CounterOpts) (*prometheus.CounterVec, error) {
	vec := prometheus.NewCounterVec(opts, consumerLabels)

	err := reg.Register(vec)
	if err == nil {
		return vec, nil
	}

	var existing prometheus.AlreadyRegisteredError
	if !errors.As(err, &existing) {
		return nil, fmt.Errorf("kafkax: register %s: %w", opts.Name, err)
	}

	vec, ok := existing.ExistingCollector.(*prometheus.CounterVec)
	if !ok {
		return nil, fmt.Errorf("kafkax: %s is registered as %T, want a counter vector",
			opts.Name, existing.ExistingCollector)
	}

	return vec, nil
}
