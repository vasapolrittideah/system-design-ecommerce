// Command server runs the inventory gRPC server.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	inventoryv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/inventory/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/server"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/observability"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/bootstrap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "inventory: %v\n", err)
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

	srv := server.MustNew(cfg.GRPC,
		server.WithLogger(log),
		server.WithRegisterer(obs.Registry()),
	)

	inventoryv1.RegisterInventoryServiceServer(srv.Registrar(), bootstrap.NewInventoryHandler(pool, cfg))

	serveErr := srv.Serve(ctx)

	// Shut down in the reverse of the order things were built, and only after
	// Serve has returned: the pool has to outlive the calls still draining, and
	// telemetry has to outlive both, or the last spans before a shutdown — the
	// interesting ones — are never flushed.
	pool.Close()

	// ctx is already cancelled by the time this runs, and Shutdown given a
	// cancelled context skips the flush it exists to perform.
	shutdownErr := obs.Shutdown(context.WithoutCancel(ctx))

	if err := errors.Join(serveErr, shutdownErr); err != nil {
		return err
	}

	log.Info("inventory stopped cleanly")

	return nil
}
