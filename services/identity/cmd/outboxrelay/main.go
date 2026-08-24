// Command outboxrelay publishes identity's outbox rows to Kafka.
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
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/kafkax"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/observability"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/outbox"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/bootstrap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "identity-outboxrelay: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.MustLoad[bootstrap.RelayConfig]()

	log := logger.MustNew(cfg.Log)
	defer func() { _ = logger.Sync(log) }()

	// First, as in every main here. Nothing in this process builds a gRPC
	// server, but the relay's spans and its backlog metric are the only thing
	// that shows it has stopped, and a telemetry stack started later exports
	// neither.
	obs := observability.MustStart(ctx, cfg.Obs, observability.WithLogger(log))
	log.Info("telemetry started", zap.String("admin_addr", obs.AdminAddr()))

	pool := postgres.MustNew(ctx, cfg.DB, postgres.WithLogger(log))
	obs.AddReadinessCheck("postgres", pool.Ping)

	publisher, err := kafkax.NewPublisher(cfg.Kafka)
	if err != nil {
		return err
	}

	// Deliberately no readiness check for the broker. A relay that cannot
	// publish is not a relay that should be restarted or taken out of anything:
	// the rows stay in the table, the next cycle tries again, and what notices
	// an outage lasting is the alert on outbox backlog. Failing readiness here
	// would turn a broker upgrade into a crash-loop that publishes nothing
	// afterwards either.
	relay, err := outbox.NewRelay(pool, publisher, cfg.Outbox,
		outbox.WithRegisterer(obs.Registry()),
	)
	if err != nil {
		return err
	}

	log.Info("relay started",
		zap.Strings("brokers", cfg.Kafka.Brokers),
		zap.Int("batch_size", cfg.Outbox.BatchSize),
	)

	// Returns on ctx cancellation, having rolled back whatever cycle was in
	// flight — those rows are still unpublished and the next process finds
	// them, which is the same path a crash takes.
	runErr := relay.Run(logger.Into(ctx, log))

	closeErr := publisher.Close()
	pool.Close()
	shutdownErr := obs.Shutdown(context.WithoutCancel(ctx))

	if err := errors.Join(runErr, closeErr, shutdownErr); err != nil {
		return err
	}

	log.Info("identity-outboxrelay stopped cleanly")

	return nil
}
