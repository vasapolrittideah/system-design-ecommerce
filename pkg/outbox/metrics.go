package outbox

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// metrics is what makes a stalled relay visible.
//
// A relay that has stopped publishing breaks nothing that anyone can see: the
// API answers, the database is healthy, readiness stays green, and events
// simply never leave. These four series are the only place that shows up.
type metrics struct {
	backlog   prometheus.Gauge
	oldestAge prometheus.Gauge
	published prometheus.Counter
	failures  prometheus.Counter
}

// newMetrics builds and registers the collectors.
func newMetrics(reg prometheus.Registerer) (*metrics, error) {
	m := &metrics{
		backlog: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "outbox_backlog_rows",
			Help: "Outbox rows written but not yet published.",
		}),

		// Depth alone cannot tell a busy relay from a dead one: a thousand rows
		// a second old is a healthy burst, while five rows ten minutes old is a
		// relay that stopped. Age is what an alert should fire on.
		oldestAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "outbox_oldest_unpublished_seconds",
			Help: "Age of the oldest unpublished outbox row.",
		}),

		published: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "outbox_published_total",
			Help: "Outbox rows published to the broker.",
		}),

		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "outbox_publish_failures_total",
			Help: "Publish cycles that failed and were rolled back.",
		}),
	}

	for _, c := range []prometheus.Collector{m.backlog, m.oldestAge, m.published, m.failures} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("outbox: register metrics: %w", err)
		}
	}

	return m, nil
}
