// Command worker runs order's own consumer of its order events: the half of
// checkout that turns a reservation into a sale once the money is in, and
// gives it back once the order is cancelled.
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
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/kafkax"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/observability"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/bootstrap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "order-worker: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.MustLoad[bootstrap.WorkerConfig]()

	log := logger.MustNew(cfg.Log)
	defer func() { _ = logger.Sync(log) }()

	// First, as in every main here — see cmd/server for why.
	obs := observability.MustStart(ctx, cfg.Obs, observability.WithLogger(log))
	log.Info("telemetry started", zap.String("admin_addr", obs.AdminAddr()))

	pool := postgres.MustNew(ctx, cfg.DB, postgres.WithLogger(log))
	obs.AddReadinessCheck("postgres", pool.Ping)

	// Deliberately no readiness check on inventory, for the reason cmd/server
	// has none either: an unreachable dependency belongs to the alert on DLQ
	// depth, not to every replica of this process leaving the endpoint list —
	// which would not even help, since nothing routes to this process.
	inventoryConn := client.MustDial(cfg.Inventory, client.WithLogger(log))

	dlq, err := kafkax.NewPublisher(cfg.Kafka)
	if err != nil {
		return err
	}

	consumer, err := kafkax.NewConsumer(cfg.Kafka.Brokers, cfg.Consumer, dlq,
		kafkax.WithRegisterer(obs.Registry()),
	)
	if err != nil {
		return err
	}

	handler := bootstrap.NewCheckoutSagaConsumer(pool, inventoryConn)

	log.Info("consumer started",
		zap.Strings("brokers", cfg.Kafka.Brokers),
		zap.String("topic", cfg.Consumer.Topic),
		zap.String("group_id", cfg.Consumer.GroupID),
	)

	runErr := consumer.Run(logger.Into(ctx, log), handler.Handle)

	// Shut down in the reverse of the order things were built, and only after
	// Run has returned — the pool and the connection have to outlive whatever
	// was still in flight when ctx was cancelled.
	closeErr := consumer.Close()
	dlqCloseErr := dlq.Close()
	pool.Close()
	if connErr := inventoryConn.Close(); connErr != nil {
		log.Warn("closing the inventory connection failed", zap.Error(connErr))
	}
	shutdownErr := obs.Shutdown(context.WithoutCancel(ctx))

	if err := errors.Join(runErr, closeErr, dlqCloseErr, shutdownErr); err != nil {
		return err
	}

	log.Info("order-worker stopped cleanly")

	return nil
}
