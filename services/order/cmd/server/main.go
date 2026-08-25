// Command server runs the order gRPC server.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	orderv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/order/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/client"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/server"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/observability"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/bootstrap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "order: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// SIGTERM is what Kubernetes sends; without it the pod is only ever
	// SIGKILLed after the grace period and none of the draining below runs.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.MustLoad[bootstrap.Config]()

	log := logger.MustNew(cfg.Log)
	defer func() { _ = logger.Sync(log) }()

	// This has to come before the gRPC server exists. otelgrpc resolves
	// otel.GetTracerProvider() when its handler is built, so a server
	// constructed first captures the no-op provider permanently — no spans, no
	// trace_id, and no error anywhere saying so.
	obs := observability.MustStart(ctx, cfg.Obs, observability.WithLogger(log))
	log.Info("telemetry started", zap.String("admin_addr", obs.AdminAddr()))

	pool := postgres.MustNew(ctx, cfg.DB, postgres.WithLogger(log))

	// Registered as the dependency is wired, not at the end: until a check is
	// added /readyz answers ready, and that gap is a window in which the pod
	// takes traffic it may not be able to serve.
	obs.AddReadinessCheck("postgres", pool.Ping)

	// Deliberately no readiness check on either connection. A dependency that
	// is unreachable is a checkout that fails with a reason code, which is a
	// better answer than every replica of this service leaving the endpoint
	// list at once and taking the order history down with it.
	conns := bootstrap.Conns{
		Inventory: client.MustDial(cfg.Inventory, client.WithLogger(log)),
		Catalog:   client.MustDial(cfg.Catalog, client.WithLogger(log)),
	}

	srv := server.MustNew(cfg.GRPC,
		server.WithLogger(log),
		server.WithRegisterer(obs.Registry()),
	)

	orderv1.RegisterOrderServiceServer(srv.Registrar(), bootstrap.NewOrderHandler(pool, conns))

	serveErr := srv.Serve(ctx)

	// Shut down in the reverse of the order things were built, and only after
	// Serve has returned: the pool has to outlive the calls still draining, and
	// telemetry has to outlive both, or the last spans before a shutdown — the
	// interesting ones — are never flushed.
	pool.Close()

	for _, conn := range []*grpc.ClientConn{conns.Inventory, conns.Catalog} {
		if err := conn.Close(); err != nil {
			log.Warn("closing a client connection failed", zap.Error(err))
		}
	}

	// ctx is already cancelled by the time this runs, and Shutdown given a
	// cancelled context skips the flush it exists to perform.
	shutdownErr := obs.Shutdown(context.WithoutCancel(ctx))

	if err := errors.Join(serveErr, shutdownErr); err != nil {
		return err
	}

	log.Info("order stopped cleanly")

	return nil
}
