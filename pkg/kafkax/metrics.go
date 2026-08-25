package kafkax

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// consumerMetrics is what makes a consumer giving up on messages visible. A
// message on a DLQ breaks nothing that anyone can see at the topic it left —
// the partition keeps draining, the group stays healthy — so this counter is
// the only place a growing DLQ shows up before somebody goes looking for it.
type consumerMetrics struct {
	processed prometheus.Counter
	dlq       prometheus.Counter
}

func newConsumerMetrics(reg prometheus.Registerer) (*consumerMetrics, error) {
	m := &consumerMetrics{
		processed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "kafka_consumer_messages_processed_total",
			Help: "Messages a consumer's handler accepted.",
		}),

		dlq: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "kafka_consumer_dlq_messages_total",
			Help: "Messages routed to a topic's .dlq after exhausting every retry.",
		}),
	}

	for _, c := range []prometheus.Collector{m.processed, m.dlq} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("kafkax: register consumer metrics: %w", err)
		}
	}

	return m, nil
}
