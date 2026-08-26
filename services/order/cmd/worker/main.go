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
	"sync"
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

	handlers := bootstrap.NewCheckoutSagaConsumers(pool, inventoryConn)

	// Two subscriptions in one process, because a kafkax.Consumer reads one
	// topic and this service reads two. One process rather than two
	// Deployments because they are two halves of one saga driving one use
	// case: the reason a background loop gets a workload of its own is that a
	// goroutine per replica multiplies the work, and a consumer group does not
	// — Kafka assigns each partition to exactly one member.
	subscriptions := []struct {
		name    string
		cfg     kafkax.ConsumerConfig
		handler kafkax.Handler
	}{
		{"order-events", cfg.Consumer, handlers.OrderEvents.Handle},
		{"payment-events", cfg.PaymentConsumer, handlers.PaymentEvents.Handle},
	}

	ctx = logger.Into(ctx, log)

	var (
		wg        sync.WaitGroup
		runErrs   = make([]error, len(subscriptions))
		consumers = make([]*kafkax.Consumer, 0, len(subscriptions))
	)

	for i, subscription := range subscriptions {
		consumer, err := kafkax.NewConsumer(cfg.Kafka.Brokers, subscription.cfg, dlq,
			kafkax.WithRegisterer(obs.Registry()),
		)
		if err != nil {
			return err
		}

		consumers = append(consumers, consumer)

		log.Info("consumer started",
			zap.String("subscription", subscription.name),
			zap.Strings("brokers", cfg.Kafka.Brokers),
			zap.String("topic", subscription.cfg.Topic),
			zap.String("group_id", subscription.cfg.GroupID),
		)

		wg.Add(1)

		go func() {
			defer wg.Done()

			runErrs[i] = consumer.Run(ctx, subscription.handler)
		}()
	}

	// Both stop on the same cancelled context, so this returns when the last
	// in-flight message has been handled rather than when the first consumer
	// notices.
	wg.Wait()

	// Shut down in the reverse of the order things were built, and only after
	// both have returned — the pool and the connection have to outlive whatever
	// was still in flight when ctx was cancelled.
	closeErrs := make([]error, 0, len(consumers))
	for _, consumer := range consumers {
		closeErrs = append(closeErrs, consumer.Close())
	}

	dlqCloseErr := dlq.Close()
	pool.Close()
	if connErr := inventoryConn.Close(); connErr != nil {
		log.Warn("closing the inventory connection failed", zap.Error(connErr))
	}
	shutdownErr := obs.Shutdown(context.WithoutCancel(ctx))

	if err := errors.Join(
		errors.Join(runErrs...),
		errors.Join(closeErrs...),
		dlqCloseErr,
		shutdownErr,
	); err != nil {
		return err
	}

	log.Info("order-worker stopped cleanly")

	return nil
}
