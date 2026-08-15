package server

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// latencyBuckets are chosen for the timeout budget this system runs on — Kong
// 5s, BFF 800ms, downstream 300ms. The resolution sits below 300ms, where the
// decisions are, and the long tail exists only to make timeouts visible.
var latencyBuckets = []float64{
	0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// metrics is the RED set — rate, errors, duration — per endpoint, plus the two
// counters that say something is wrong rather than slow.
type metrics struct {
	handled  *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight *prometheus.GaugeVec
	panics   *prometheus.CounterVec
}

// newMetrics builds and registers the collectors.
//
// The duration histogram deliberately carries no code label: rate and errors
// come off the counter, and multiplying thirteen buckets by every status code a
// service can return is how a metrics backend drowns.
func newMetrics(reg prometheus.Registerer) (*metrics, error) {
	m := &metrics{
		handled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "grpc_server_handled_total",
			Help: "Total gRPC requests completed, by status code.",
		}, []string{"grpc_type", "grpc_service", "grpc_method", "grpc_code"}),

		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "grpc_server_handling_seconds",
			Help:    "Wall time taken to handle a gRPC request.",
			Buckets: latencyBuckets,
		}, []string{"grpc_type", "grpc_service", "grpc_method"}),

		inFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "grpc_server_in_flight_requests",
			Help: "gRPC requests currently being handled.",
		}, []string{"grpc_type", "grpc_service", "grpc_method"}),

		panics: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "grpc_server_panics_recovered_total",
			Help: "Panics recovered by the gRPC recovery interceptor.",
		}, []string{"grpc_service", "grpc_method"}),
	}

	for _, c := range []prometheus.Collector{m.handled, m.duration, m.inFlight, m.panics} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("grpcx/server: register metrics: %w", err)
		}
	}

	return m, nil
}

// observe records one completed call.
func (m *metrics) observe(callType, service, method string, err error, elapsed time.Duration) {
	m.handled.WithLabelValues(callType, service, method, status.Code(err).String()).Inc()
	m.duration.WithLabelValues(callType, service, method).Observe(elapsed.Seconds())
}

func metricsUnary(m *metrics) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		service, method := splitMethod(info.FullMethod)

		inFlight := m.inFlight.WithLabelValues(typeUnary, service, method)
		inFlight.Inc()
		defer inFlight.Dec()

		start := time.Now()
		resp, err := handler(ctx, req)
		m.observe(typeUnary, service, method, err, time.Since(start))

		return resp, err
	}
}

func metricsStream(m *metrics) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		service, method := splitMethod(info.FullMethod)
		callType := streamType(info)

		inFlight := m.inFlight.WithLabelValues(callType, service, method)
		inFlight.Inc()
		defer inFlight.Dec()

		start := time.Now()
		err := handler(srv, stream)
		m.observe(callType, service, method, err, time.Since(start))

		return err
	}
}
