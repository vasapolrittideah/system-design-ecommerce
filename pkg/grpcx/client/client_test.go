package client_test

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/client"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/internal/grpctest"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
)

// testConfig goes through pkg/config rather than a struct literal: the
// behaviour under test lives in the `envDefault` tags, and a literal would
// silently disable the very retries and breakers these tests assert on.
func testConfig(t *testing.T, target string, overrides map[string]string) client.Config {
	t.Helper()

	environ := map[string]string{"TARGET": target}
	for k, v := range overrides {
		environ[k] = v
	}

	cfg, err := config.Load[client.Config](config.WithEnviron(environ))
	if err != nil {
		t.Fatalf("load client config: %v", err)
	}

	return cfg
}

// serve starts a plain gRPC server — no grpcx/server chain — so that what these
// tests observe is the client's own behaviour and nothing else.
func serve(t *testing.T, handlers map[string]grpctest.Handler) string {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := grpc.NewServer()
	grpctest.New(handlers).Register(srv)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(lis)
	}()

	t.Cleanup(func() {
		srv.Stop()
		<-done
	})

	return lis.Addr().String()
}

func dial(t *testing.T, target string, overrides map[string]string) *grpc.ClientConn {
	t.Helper()

	conn, err := client.Dial(testConfig(t, target, overrides))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return conn
}

// A read that fails because the pod went away mid-deploy should not reach the
// caller as an error. Retrying is free here: the call had no effect.
func TestRetriesIdempotentMethod(t *testing.T) {
	var calls atomic.Int32
	target := serve(t, map[string]grpctest.Handler{
		"GetFlaky": func(context.Context, string) (string, error) {
			if calls.Add(1) < 3 {
				return "", status.Error(codes.Unavailable, "restarting")
			}

			return "recovered", nil
		},
	})

	conn := dial(t, target, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := grpctest.Call(ctx, conn, grpctest.MethodGetFlaky, "")
	if err != nil {
		t.Fatalf("GetFlaky: %v", err)
	}
	if got != "recovered" {
		t.Errorf("response = %q, want %q", got, "recovered")
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("attempts = %d, want 3", n)
	}
}

// A method that changes state is not retried, whatever the failure looked like.
// Retrying PlaceOrder after an ambiguous failure is how a customer ends up with
// two orders; that case belongs to an idempotency key, not to the transport.
func TestDoesNotRetryStateChangingMethod(t *testing.T) {
	var calls atomic.Int32
	target := serve(t, map[string]grpctest.Handler{
		"DoWork": func(context.Context, string) (string, error) {
			calls.Add(1)

			return "", status.Error(codes.Unavailable, "restarting")
		},
	})

	conn := dial(t, target, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := grpctest.Call(ctx, conn, grpctest.MethodDoWork, ""); status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %s, want %s", status.Code(err), codes.Unavailable)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("attempts = %d, want 1", n)
	}
}

// Only failures that say nothing about the request are retried. A NotFound is
// an answer, and asking again produces the same one at three times the cost.
func TestDoesNotRetryBusinessError(t *testing.T) {
	var calls atomic.Int32
	target := serve(t, map[string]grpctest.Handler{
		"GetFlaky": func(context.Context, string) (string, error) {
			calls.Add(1)

			return "", status.Error(codes.NotFound, "no such order")
		},
	})

	conn := dial(t, target, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := grpctest.Call(ctx, conn, grpctest.MethodGetFlaky, ""); status.Code(err) != codes.NotFound {
		t.Fatalf("code = %s, want %s", status.Code(err), codes.NotFound)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("attempts = %d, want 1", n)
	}
}

// Retries live inside the caller's budget, not beside it. Three attempts that
// each wait for the full deadline would turn a 300ms budget into 900ms.
func TestRetriesStayInsideCallerDeadline(t *testing.T) {
	var calls atomic.Int32
	target := serve(t, map[string]grpctest.Handler{
		"GetFlaky": func(ctx context.Context, _ string) (string, error) {
			calls.Add(1)
			<-ctx.Done()

			return "", status.Error(codes.DeadlineExceeded, "too slow")
		},
	})

	conn := dial(t, target, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := grpctest.Call(ctx, conn, grpctest.MethodGetFlaky, "")
	elapsed := time.Since(start)

	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("code = %s, want %s", status.Code(err), codes.DeadlineExceeded)
	}
	if elapsed > time.Second {
		t.Errorf("call took %s, well past the 150ms budget it was given", elapsed)
	}
}

// A caller that sets no deadline still gets one. An unbounded gRPC call waits
// forever, and forever is long enough for every goroutine behind it to pile up.
func TestAppliesDefaultDeadline(t *testing.T) {
	target := serve(t, map[string]grpctest.Handler{
		"GetEcho": func(ctx context.Context, _ string) (string, error) {
			<-ctx.Done()

			return "", ctx.Err()
		},
	})

	conn := dial(t, target, map[string]string{"TIMEOUT": "100ms"})

	start := time.Now()
	_, err := grpctest.Call(context.Background(), conn, grpctest.MethodGetEcho, "")
	elapsed := time.Since(start)

	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("code = %s, want %s", status.Code(err), codes.DeadlineExceeded)
	}
	if elapsed > 2*time.Second {
		t.Errorf("call took %s; no default deadline appears to have been applied", elapsed)
	}
}

// A caller with its own budget keeps it — the default fills a gap, it does not
// override the one place that knows where in the cascade this call sits.
func TestKeepsCallerDeadline(t *testing.T) {
	target := serve(t, map[string]grpctest.Handler{
		"GetEcho": func(ctx context.Context, _ string) (string, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				return "none", nil
			}
			// Reported coarsely: the exact remaining budget depends on how
			// long the call spent in flight.
			if time.Until(deadline) > time.Second {
				return "caller", nil
			}

			return "default", nil
		},
	})

	conn := dial(t, target, map[string]string{"TIMEOUT": "100ms"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := grpctest.Call(ctx, conn, grpctest.MethodGetEcho, "")
	if err != nil {
		t.Fatalf("GetEcho: %v", err)
	}
	if got != "caller" {
		t.Errorf("deadline seen by the callee = %q, want %q", got, "caller")
	}
}

// The identity and the correlation ID have to survive the hop, or a service
// three calls in cannot say whose request it is serving or which operation it
// belongs to.
func TestPropagatesIdentityAndCorrelationID(t *testing.T) {
	target := serve(t, map[string]grpctest.Handler{
		"GetEcho": func(ctx context.Context, _ string) (string, error) {
			md, _ := metadata.FromIncomingContext(ctx)

			return strings.Join([]string{
				strings.Join(md.Get(grpcx.MetadataCorrelationID), ""),
				strings.Join(md.Get(grpcx.MetadataUserID), ""),
				strings.Join(md.Get(grpcx.MetadataUserRoles), ""),
			}, "|"), nil
		},
	})

	conn := dial(t, target, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = logger.WithCorrelationID(ctx, "corr-1")
	ctx = grpcx.IdentityInto(ctx, grpcx.Identity{UserID: "u-1", Roles: []string{"customer", "admin"}})

	got, err := grpctest.Call(ctx, conn, grpctest.MethodGetEcho, "")
	if err != nil {
		t.Fatalf("GetEcho: %v", err)
	}
	if want := "corr-1|u-1|customer,admin"; got != want {
		t.Errorf("metadata seen by the callee = %q, want %q", got, want)
	}
}

// Once a target is established to be down, calls should stop going out — that
// is the whole point of the breaker, and it must report itself as Unavailable
// so the caller handles it exactly as it would a real outage.
func TestBreakerOpensOnSustainedFailure(t *testing.T) {
	var calls atomic.Int32
	target := serve(t, map[string]grpctest.Handler{
		"DoWork": func(context.Context, string) (string, error) {
			calls.Add(1)

			return "", status.Error(codes.Internal, "broken")
		},
	})

	conn := dial(t, target, map[string]string{
		"BREAKER_MIN_REQUESTS":  "4",
		"BREAKER_FAILURE_RATIO": "0.5",
		"BREAKER_TIMEOUT":       "1m",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for range 4 {
		if _, err := grpctest.Call(ctx, conn, grpctest.MethodDoWork, ""); status.Code(err) != codes.Internal {
			t.Fatalf("code = %s, want %s", status.Code(err), codes.Internal)
		}
	}

	_, err := grpctest.Call(ctx, conn, grpctest.MethodDoWork, "")
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code after the breaker should have opened = %s, want %s", status.Code(err), codes.Unavailable)
	}
	if n := calls.Load(); n != 4 {
		t.Errorf("the server saw %d calls, want 4 — the breaker let one through while open", n)
	}
}

// Business errors are the service working. Counting a run of sold-out SKUs as
// failures would open the breaker and take checkout down for everyone.
func TestBreakerIgnoresBusinessErrors(t *testing.T) {
	target := serve(t, map[string]grpctest.Handler{
		"DoWork": func(context.Context, string) (string, error) {
			return "", status.Error(codes.FailedPrecondition, "out of stock")
		},
	})

	conn := dial(t, target, map[string]string{
		"BREAKER_MIN_REQUESTS":  "4",
		"BREAKER_FAILURE_RATIO": "0.5",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := range 10 {
		if _, err := grpctest.Call(ctx, conn, grpctest.MethodDoWork, ""); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("call %d: code = %s, want %s", i, status.Code(err), codes.FailedPrecondition)
		}
	}
}

// A target that does not resolve must not stop the process from starting: during
// a rolling deploy every dependency is briefly unreachable, and a service that
// refuses to boot then turns one failure into two.
func TestDialDoesNotBlockOnUnreachableTarget(t *testing.T) {
	conn, err := client.Dial(testConfig(t, "dns:///nonexistent.invalid:50051", nil))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
}
