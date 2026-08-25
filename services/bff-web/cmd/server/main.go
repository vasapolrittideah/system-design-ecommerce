// Command server runs the web BFF's HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/client"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/observability"
	"github.com/vasapolrittideah/system-design-ecommerce/services/bff-web/internal/bootstrap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "bff-web: %v\n", err)
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

	// This has to come before anything instrumented exists. otelhttp and
	// otelgrpc resolve otel.GetTracerProvider() when their handlers are built,
	// so a server or a client constructed first captures the no-op provider
	// permanently — no spans, no trace_id, and no error anywhere saying so.
	//
	// It matters more here than anywhere else in the repo: this process is where
	// a trace begins, so getting the order wrong leaves every downstream span
	// parented to nothing.
	obs := observability.MustStart(ctx, cfg.Obs, observability.WithLogger(log))
	log.Info("telemetry started", zap.String("admin_addr", obs.AdminAddr()))

	// One connection per target service, dialled here and shared by every
	// request. Dialling does not block on connectivity, so a service that is
	// down at this moment costs nothing at startup — its first calls fail with
	// Unavailable, which is what the retry and the breaker are for.
	conns := bootstrap.Conns{
		Identity:  client.MustDial(cfg.Identity, client.WithLogger(log)),
		Catalog:   client.MustDial(cfg.Catalog, client.WithLogger(log)),
		Inventory: client.MustDial(cfg.Inventory, client.WithLogger(log)),
		Order:     client.MustDial(cfg.Order, client.WithLogger(log)),
	}

	// Deliberately no readiness check on any of them.
	//
	// A dependency belongs in readiness when this pod cannot serve without it.
	// These can: an unreachable service comes back as a 503 carrying a reason
	// code, which is a better answer than vanishing from Kong's endpoint list.
	// Gating on it would also mean every BFF replica going NotReady together
	// during a downstream's own rolling deploy — turning one service's routine
	// restart into an outage with nothing left to route to.
	srv := httpx.MustNewServer(cfg.HTTP,
		bootstrap.NewRouter(cfg, conns, log, obs.Registry()),
		httpx.WithServerLogger(log),
	)

	serveErr := srv.Serve(ctx)

	// Shut down in the reverse of the order things were built, and only after
	// Serve has returned: the connections have to outlive the requests still
	// draining, and telemetry has to outlive both, or the last spans before a
	// shutdown — the interesting ones — are never flushed.
	closeErr := errors.Join(conns.Identity.Close(), conns.Catalog.Close(), conns.Inventory.Close())

	// ctx is already cancelled by the time this runs, and Shutdown given a
	// cancelled context skips the flush it exists to perform.
	shutdownErr := obs.Shutdown(context.WithoutCancel(ctx))

	if err := errors.Join(serveErr, closeErr, shutdownErr); err != nil {
		return err
	}

	log.Info("bff-web stopped cleanly")

	return nil
}
