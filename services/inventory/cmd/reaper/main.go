// Command reaper returns the stock of reservations nobody committed in time.
//
// It runs as its own workload rather than as a goroutine in the server, because
// the two scale for different reasons: the server's replica count follows request
// traffic, and a sweep that ran once per replica would multiply the row locks it
// takes by however many pods the autoscaler decided on.
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
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/observability"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/bootstrap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "inventory-reaper: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.MustLoad[bootstrap.Config]()

	log := logger.MustNew(cfg.Log)
	defer func() { _ = logger.Sync(log) }()

	obs := observability.MustStart(ctx, cfg.Obs, observability.WithLogger(log))
	log.Info("telemetry started", zap.String("admin_addr", obs.AdminAddr()))

	pool := postgres.MustNew(ctx, cfg.DB, postgres.WithLogger(log))

	obs.AddReadinessCheck("postgres", pool.Ping)

	sweeper, err := bootstrap.NewReaper(pool, cfg, obs.Registry())
	if err != nil {
		return err
	}

	// The plain logger, never the result of logger.From: storing what From
	// returned would duplicate its context fields on the next hop.
	runErr := sweeper.Run(logger.Into(ctx, log))

	pool.Close()

	shutdownErr := obs.Shutdown(context.WithoutCancel(ctx))

	if err := errors.Join(runErr, shutdownErr); err != nil {
		return err
	}

	log.Info("inventory reaper stopped cleanly")

	return nil
}
