// Command timeoutworker ends orders nobody paid for in time.
//
// It is what makes an unpaid order end at all. A declined card leaves the order
// open so the customer can try another, so nothing in the event flow ever
// concludes that an order is over — running out of time is the only thing that
// does, and cancelling raises OrderCancelled, which the worker's own
// subscription turns into a released reservation.
//
// It runs as its own workload rather than as a goroutine in the server, because
// the two scale for different reasons: the server's replica count follows
// request traffic, and a sweep that ran once per replica would multiply the row
// locks it takes by however many pods the autoscaler decided on. That is the
// difference between this and the two Kafka subscriptions, which do share a
// process — a consumer group hands each partition to exactly one member, and a
// timer has no such thing.
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
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/bootstrap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "order-timeoutworker: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.MustLoad[bootstrap.TimeoutConfig]()

	log := logger.MustNew(cfg.Log)
	defer func() { _ = logger.Sync(log) }()

	// First, as in every main here — see cmd/server for why.
	obs := observability.MustStart(ctx, cfg.Obs, observability.WithLogger(log))
	log.Info("telemetry started", zap.String("admin_addr", obs.AdminAddr()))

	pool := postgres.MustNew(ctx, cfg.DB, postgres.WithLogger(log))
	obs.AddReadinessCheck("postgres", pool.Ping)

	worker, err := bootstrap.NewSagaTimeoutWorker(pool, cfg, obs.Registry())
	if err != nil {
		return err
	}

	log.Info("saga timeout worker started",
		zap.Duration("payment_window", cfg.PaymentWindow),
		zap.Duration("interval", cfg.Timeout.Interval),
		zap.Int("batch_size", cfg.Timeout.BatchSize),
	)

	// The plain logger, never the result of logger.From: storing what From
	// returned would duplicate its context fields on the next hop.
	runErr := worker.Run(logger.Into(ctx, log))

	pool.Close()

	shutdownErr := obs.Shutdown(context.WithoutCancel(ctx))

	if err := errors.Join(runErr, shutdownErr); err != nil {
		return err
	}

	log.Info("order-timeoutworker stopped cleanly")

	return nil
}
