package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/internal/grpctest"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/server"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

// testConfig goes through pkg/config rather than a struct literal, because the
// defaults these tests depend on live in `envDefault` tags and a literal would
// silently get zero values for every field it forgot — a zero MaxRecvMsgSize
// rejects every request, and a zero ShutdownTimeout makes every stop a kill.
func testConfig(t *testing.T, overrides map[string]string) server.Config {
	t.Helper()

	environ := map[string]string{"ADDR": "127.0.0.1:0"}
	for k, v := range overrides {
		environ[k] = v
	}

	cfg, err := config.Load[server.Config](config.WithEnviron(environ))
	if err != nil {
		t.Fatalf("load server config: %v", err)
	}

	return cfg
}

// syncBuffer collects log output from every goroutine the server runs on. A
// bare bytes.Buffer races here: the accept loop, each request, and the test
// itself all touch it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

type harness struct {
	conn *grpc.ClientConn
	reg  *prometheus.Registry
	logs *syncBuffer
}

// start brings up a server on a loopback port and dials it with a bare
// connection — bare so that what these tests exercise is the server chain
// alone, not grpcx/client's retries papering over it.
func start(t *testing.T, handlers map[string]grpctest.Handler, opts ...server.Option) harness {
	t.Helper()

	reg := prometheus.NewRegistry()
	logs := &syncBuffer{}
	log := logger.MustNew(
		logger.Config{
			Level:   zapcore.DebugLevel,
			Format:  logger.FormatJSON,
			Service: "grpcx-test",
			// Sampling off: these tests read the lines they assert on, and a
			// sampler is allowed to drop them.
			StacktraceLevel: zapcore.ErrorLevel,
		},
		logger.WithWriter(logs),
	)

	opts = append([]server.Option{server.WithRegisterer(reg), server.WithLogger(log)}, opts...)

	srv, err := server.New(testConfig(t, nil), opts...)
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	grpctest.New(handlers).Register(srv.Registrar())

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx) }()

	conn, err := grpc.NewClient(srv.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		cancel()
		t.Fatalf("dial server: %v", err)
	}

	t.Cleanup(func() {
		conn.Close()
		cancel()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("Serve returned an error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after its context was cancelled")
		}
	})

	return harness{conn: conn, reg: reg, logs: logs}
}

func echoHandler(fn func(ctx context.Context) string) grpctest.Handler {
	return func(ctx context.Context, _ string) (string, error) {
		return fn(ctx), nil
	}
}

// The correlation ID is what ties one user-visible operation together across
// every gRPC hop and every Kafka event it causes, so an inbound one has to be
// adopted rather than replaced.
func TestChainAdoptsInboundCorrelationID(t *testing.T) {
	h := start(t, map[string]grpctest.Handler{
		"GetEcho": echoHandler(logger.CorrelationID),
	})

	ctx := metadata.AppendToOutgoingContext(context.Background(), grpcx.MetadataCorrelationID, "corr-1")

	var header metadata.MD
	got, err := grpctest.Call(ctx, h.conn, grpctest.MethodGetEcho, "", grpc.Header(&header))
	if err != nil {
		t.Fatalf("GetEcho: %v", err)
	}
	if got != "corr-1" {
		t.Errorf("correlation ID in handler context = %q, want %q", got, "corr-1")
	}
	if values := header.Get(grpcx.MetadataCorrelationID); len(values) != 1 || values[0] != "corr-1" {
		t.Errorf("correlation ID response header = %v, want [corr-1]", values)
	}
}

// A call that arrives without one still needs an ID, or the first hop of an
// operation is the one hop nobody can trace.
func TestChainMintsMissingCorrelationID(t *testing.T) {
	h := start(t, map[string]grpctest.Handler{
		"GetEcho": echoHandler(logger.CorrelationID),
	})

	var header metadata.MD
	got, err := grpctest.Call(context.Background(), h.conn, grpctest.MethodGetEcho, "", grpc.Header(&header))
	if err != nil {
		t.Fatalf("GetEcho: %v", err)
	}
	if got == "" {
		t.Fatal("no correlation ID was minted for a call that arrived without one")
	}
	if values := header.Get(grpcx.MetadataCorrelationID); len(values) != 1 || values[0] != got {
		t.Errorf("correlation ID response header = %v, want [%s]", values, got)
	}
}

func TestChainExtractsIdentity(t *testing.T) {
	h := start(t, map[string]grpctest.Handler{
		"GetEcho": echoHandler(func(ctx context.Context) string {
			id, ok := grpcx.IdentityFrom(ctx)
			if !ok {
				return "none"
			}

			return id.UserID + ":" + strings.Join(id.Roles, "+")
		}),
	})

	ctx := metadata.AppendToOutgoingContext(context.Background(),
		grpcx.MetadataUserID, "u-1",
		grpcx.MetadataUserRoles, "customer,admin",
	)

	got, err := grpctest.Call(ctx, h.conn, grpctest.MethodGetEcho, "")
	if err != nil {
		t.Fatalf("GetEcho: %v", err)
	}
	if want := "u-1:customer+admin"; got != want {
		t.Errorf("identity in handler context = %q, want %q", got, want)
	}
}

// A panic must cost the caller one request, not the pod every request it was
// serving — and the reply must say nothing about what actually broke.
func TestChainRecoversPanic(t *testing.T) {
	h := start(t, map[string]grpctest.Handler{
		"DoPanic": func(context.Context, string) (string, error) {
			panic("connection string: postgres://user:hunter2@db")
		},
		"GetEcho": echoHandler(func(context.Context) string { return "alive" }),
	})

	_, err := grpctest.Call(context.Background(), h.conn, grpctest.MethodDoPanic, "")
	st, _ := status.FromError(err)
	if st.Code() != codes.Internal {
		t.Fatalf("code = %s, want %s", st.Code(), codes.Internal)
	}
	if strings.Contains(st.Message(), "hunter2") {
		t.Errorf("the panic value leaked into the status message: %q", st.Message())
	}

	if got := counter(t, h.reg, "grpc_server_panics_recovered_total", nil); got != 1 {
		t.Errorf("panics recovered = %v, want 1", got)
	}

	// The server has to still be serving; a recovery that leaves the process
	// wedged is no better than the panic.
	if got, err := grpctest.Call(context.Background(), h.conn, grpctest.MethodGetEcho, ""); err != nil || got != "alive" {
		t.Errorf("call after panic = %q, %v; want \"alive\", nil", got, err)
	}
}

func TestChainRecordsMetrics(t *testing.T) {
	h := start(t, map[string]grpctest.Handler{
		"GetEcho": echoHandler(func(context.Context) string { return "ok" }),
		"DoWork": func(context.Context, string) (string, error) {
			return "", status.Error(codes.NotFound, "no such order")
		},
	})

	if _, err := grpctest.Call(context.Background(), h.conn, grpctest.MethodGetEcho, ""); err != nil {
		t.Fatalf("GetEcho: %v", err)
	}
	if _, err := grpctest.Call(context.Background(), h.conn, grpctest.MethodDoWork, ""); err == nil {
		t.Fatal("DoWork returned no error, want NotFound")
	}

	ok := map[string]string{"grpc_method": "GetEcho", "grpc_code": "OK"}
	if got := counter(t, h.reg, "grpc_server_handled_total", ok); got != 1 {
		t.Errorf("handled{GetEcho,OK} = %v, want 1", got)
	}

	notFound := map[string]string{"grpc_method": "DoWork", "grpc_code": "NotFound"}
	if got := counter(t, h.reg, "grpc_server_handled_total", notFound); got != 1 {
		t.Errorf("handled{DoWork,NotFound} = %v, want 1", got)
	}

	// Every completed call must land in the latency histogram, whatever it
	// returned — a p99 computed only over successes hides the slow failures.
	if got := histogramCount(t, h.reg, "grpc_server_handling_seconds", nil); got != 2 {
		t.Errorf("handling_seconds observations = %d, want 2", got)
	}

	// The gauge has to come back down, or a dashboard shows permanent load.
	if got := gauge(t, h.reg, "grpc_server_in_flight_requests", nil); got != 0 {
		t.Errorf("in-flight requests after completion = %v, want 0", got)
	}
}

// A NotFound is the system working. Logging it at error makes the error rate a
// measure of what users asked for rather than of whether the service is healthy.
func TestChainLogsExpectedOutcomesBelowError(t *testing.T) {
	h := start(t, map[string]grpctest.Handler{
		"DoWork": func(context.Context, string) (string, error) {
			return "", status.Error(codes.NotFound, "no such order")
		},
	})

	ctx := metadata.AppendToOutgoingContext(context.Background(), grpcx.MetadataCorrelationID, "corr-9")
	if _, err := grpctest.Call(ctx, h.conn, grpctest.MethodDoWork, ""); err == nil {
		t.Fatal("DoWork returned no error, want NotFound")
	}

	entry := accessLog(t, h.logs)
	if entry["level"] != "info" {
		t.Errorf("level = %v, want info", entry["level"])
	}
	if entry["grpc.code"] != codes.NotFound.String() {
		t.Errorf("grpc.code = %v, want %s", entry["grpc.code"], codes.NotFound)
	}
	if entry["correlation_id"] != "corr-9" {
		t.Errorf("correlation_id = %v, want corr-9", entry["correlation_id"])
	}
}

func TestHealthReportsServing(t *testing.T) {
	h := start(t, nil)

	resp, err := healthpb.NewHealthClient(h.conn).Check(context.Background(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("health check: %v", err)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Errorf("status = %s, want SERVING", resp.GetStatus())
	}
}

// Streaming methods go through the same chain, and something has to prove it:
// reflection and health watches are streams, so a chain that only covers unary
// leaves a panic in either one able to take the process down. Health's Watch is
// a real server stream, which makes it a usable subject without a service proto.
func TestChainCoversStreamingMethods(t *testing.T) {
	h := start(t, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := healthpb.NewHealthClient(h.conn).Watch(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("receive first status: %v", err)
	}

	header, err := stream.Header()
	if err != nil {
		t.Fatalf("stream header: %v", err)
	}
	if values := header.Get(grpcx.MetadataCorrelationID); len(values) != 1 || values[0] == "" {
		t.Errorf("correlation ID header on stream = %v, want one non-empty value", values)
	}

	// The first message is through, so the handler is provably inside the
	// chain right now — which makes this gauge deterministic rather than a
	// race against the stream shutting down.
	streamLabels := map[string]string{"grpc_type": "server_stream"}
	if got := gauge(t, h.reg, "grpc_server_in_flight_requests", streamLabels); got != 1 {
		t.Errorf("in-flight streams = %v, want 1", got)
	}
}

// New binds the port so that a misconfigured address fails while main is still
// wiring, rather than after the pool, the consumers, and the relay are up.
func TestNewFailsOnUnusableAddress(t *testing.T) {
	cfg := testConfig(t, map[string]string{"ADDR": "127.0.0.1:not-a-port"})

	srv, err := server.New(cfg, server.WithRegisterer(prometheus.NewRegistry()))
	if err == nil {
		t.Fatal("New succeeded on an unusable address")
	}
	if srv != nil {
		t.Error("New returned a server alongside its error")
	}
}

func accessLog(t *testing.T, logs *syncBuffer) map[string]any {
	t.Helper()

	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		entry := map[string]any{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("parse log line %q: %v", line, err)
		}
		if entry["msg"] == "gRPC call" {
			return entry
		}
	}

	t.Fatalf("no access log line found in:\n%s", logs.String())

	return nil
}

// matches reports whether m carries every label in want. A subset match keeps
// the assertions readable: a test that cares about the code does not have to
// restate grpc_type and grpc_service as well.
func matches(m *dto.Metric, want map[string]string) bool {
	for name, value := range want {
		found := false
		for _, pair := range m.GetLabel() {
			if pair.GetName() == name && pair.GetValue() == value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

func metricsNamed(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) []*dto.Metric {
	t.Helper()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	var out []*dto.Metric
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, m := range family.GetMetric() {
			if matches(m, labels) {
				out = append(out, m)
			}
		}
	}

	return out
}

func counter(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()

	var total float64
	for _, m := range metricsNamed(t, reg, name, labels) {
		total += m.GetCounter().GetValue()
	}

	return total
}

func gauge(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()

	var total float64
	for _, m := range metricsNamed(t, reg, name, labels) {
		total += m.GetGauge().GetValue()
	}

	return total
}

func histogramCount(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) uint64 {
	t.Helper()

	var total uint64
	for _, m := range metricsNamed(t, reg, name, labels) {
		total += m.GetHistogram().GetSampleCount()
	}

	return total
}
