// Command fakeprovider is a payment provider that can be made to misbehave.
//
// It exists because a hosted sandbox is the wrong tool for what this system
// actually has to survive. A real provider's test mode will decline a card for
// you, which is the easiest case; it will not, on demand, time out with the
// charge already made, deliver one webhook twice, or deliver the webhook before
// the response to the call that caused it. Those are the cases the saga, the
// inbox, and the FOR UPDATE in settling exist for, and this is what produces
// them.
//
// Which behaviour it takes is decided by the last two digits of the amount, so
// a test — or a person clicking through the storefront — chooses one by
// choosing what to buy rather than by configuring anything:
//
//	…00  succeed immediately
//	…01  decline immediately
//	…02  accept, then succeed by webhook after a delay
//	…03  accept, then send the success webhook twice
//	…04  send the webhook before answering the call that caused it
//	…05  never answer, so the caller's deadline is what ends the call
//
// It is not deployed to prod: the overlay that includes it is local, and the
// real provider is a base URL away.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/config"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/fakeprovider"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "payment-fakeprovider: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.MustLoad[fakeprovider.Config]()

	log := logger.MustNew(cfg.Log)
	defer func() { _ = logger.Sync(log) }()

	provider := fakeprovider.New(cfg, log)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           provider.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errs := make(chan error, 1)

	go func() {
		log.Info("fake provider listening",
			zap.String("addr", cfg.Addr),
			zap.String("callback_url", cfg.CallbackURL),
		)

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	// Webhooks are sent from goroutines this process owns, so the shutdown has
	// to outlast the longest delay one of them is sleeping through — otherwise
	// the behaviour that exists to be tested is the one a rollout cancels.
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdown); err != nil {
		return err
	}

	provider.Wait(shutdown)

	return nil
}
