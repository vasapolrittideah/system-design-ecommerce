// Package bootstrap assembles the payment service: it owns the configuration
// structs and the dependency wiring, so cmd/ stays a thin entry point and the
// order things are constructed in is decided in one reviewable place.
package bootstrap

import (
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/client"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/server"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/kafkax"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/observability"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/outbox"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/adapter/out/provider"
)

// Config is everything the server process reads from the environment.
//
// One struct with nested prefixes rather than one config.Load per dependency,
// because pkg/config reports every parse failure together: a deployment missing
// three variables says so once, instead of revealing them one restart at a time.
//
// A variable that matches no field here is ignored rather than rejected, so
// check the struct before adding one to deploy/k8s/base/payment/config.env.
//
// There is no signing key and no token policy: this service is reached over
// east-west gRPC with identity already settled a hop earlier, and the only
// question it answers about a caller is which attempts are theirs. The one
// secret it does hold is the provider's, which authenticates the provider to
// this service rather than a user.
type Config struct {
	Log  logger.Config        `envPrefix:"LOG_"`
	Obs  observability.Config `envPrefix:"OBS_"`
	GRPC server.Config        `envPrefix:"PAYMENT_GRPC_"`
	DB   postgres.Config      `envPrefix:"PAYMENT_DB_"`

	// Order is read synchronously before an attempt is written, on the critical
	// path of somebody pressing Pay, so the deadline is the caller's to set and
	// far shorter than the request it sits inside.
	Order client.Config `envPrefix:"PAYMENT_ORDER_"`

	// Provider is the one dependency here that is not another service in this
	// cluster. Its timeout is deliberately the longest of the three: a card
	// network is slower than anything east-west, and cutting it short turns an
	// answer into an ambiguous timeout this service then has to reconcile.
	Provider provider.Config `envPrefix:"PAYMENT_PROVIDER_"`
}

// RelayConfig is everything the outbox relay reads from the environment.
//
// A struct of its own though it mounts the same ConfigMap: the relay moves rows
// to a broker and calls neither the order service nor the provider, so an
// address it never dials must not be an address it fails to start without —
// and a provider secret it has no use for must not be mounted into it.
type RelayConfig struct {
	Log logger.Config        `envPrefix:"LOG_"`
	Obs observability.Config `envPrefix:"OBS_"`
	DB  postgres.Config      `envPrefix:"PAYMENT_DB_"`

	Outbox outbox.RelayConfig     `envPrefix:"OUTBOX_"`
	Kafka  kafkax.PublisherConfig `envPrefix:"KAFKA_"`
}
