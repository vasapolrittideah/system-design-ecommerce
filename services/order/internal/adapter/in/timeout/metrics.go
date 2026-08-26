package timeout

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// metrics is what makes a stalled timeout worker visible.
//
// A worker that has stopped sweeping breaks nothing anyone can see: the API
// answers, the database is healthy, readiness stays green, and orders nobody
// paid for quietly accumulate while their stock stays held until inventory's
// own reaper takes it back — by which point the order is a row that will sit in
// pending_payment forever. These two series are where that shows up.
type metrics struct {
	expired  prometheus.Counter
	failures prometheus.Counter
}

// newMetrics builds and registers the collectors.
func newMetrics(reg prometheus.Registerer) (*metrics, error) {
	m := &metrics{
		// A rate worth watching in both directions. Zero forever means the
		// sweep is not running; a sustained rise means shoppers are reaching
		// the payment screen and not finishing, which is a checkout or provider
		// problem showing up here first.
		expired: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "order_saga_timeouts_total",
			Help: "Orders cancelled because nobody paid for them in time.",
		}),

		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "order_saga_timeout_sweeps_failed_total",
			Help: "Sweeps that failed and were rolled back.",
		}),
	}

	for _, c := range []prometheus.Collector{m.expired, m.failures} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("timeout: register metrics: %w", err)
		}
	}

	return m, nil
}
