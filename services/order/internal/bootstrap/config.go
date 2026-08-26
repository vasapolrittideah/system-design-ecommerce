// Package bootstrap assembles the order service: it owns the configuration
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
)

// Config is everything the server process reads from the environment.
//
// One struct with nested prefixes rather than one config.Load per dependency,
// because pkg/config reports every parse failure together: a deployment missing
// three variables says so once, instead of revealing them one restart at a time.
//
// A variable that matches no field here is ignored rather than rejected, so
// check the struct before adding one to deploy/k8s/base/order/config.env.
//
// There is no signing key and no token policy: this service is reached over
// east-west gRPC with identity already settled a hop earlier, and the only
// question it answers about a caller is which orders are theirs.
type Config struct {
	Log  logger.Config        `envPrefix:"LOG_"`
	Obs  observability.Config `envPrefix:"OBS_"`
	GRPC server.Config        `envPrefix:"ORDER_GRPC_"`
	DB   postgres.Config      `envPrefix:"ORDER_DB_"`

	// The two services checkout fans out to. Reserving is on the critical path
	// of a shopper pressing Buy, so both deadlines are the caller's to set and
	// both are far shorter than the request they sit inside.
	Inventory client.Config `envPrefix:"ORDER_INVENTORY_"`
	Catalog   client.Config `envPrefix:"ORDER_CATALOG_"`
}

// RelayConfig is everything the outbox relay reads from the environment.
//
// A struct of its own though it mounts the same ConfigMap: the relay moves rows
// to a broker and calls no other service, so a gateway address it never dials
// must not be an address it fails to start without.
type RelayConfig struct {
	Log logger.Config        `envPrefix:"LOG_"`
	Obs observability.Config `envPrefix:"OBS_"`
	DB  postgres.Config      `envPrefix:"ORDER_DB_"`

	Outbox outbox.RelayConfig     `envPrefix:"OUTBOX_"`
	Kafka  kafkax.PublisherConfig `envPrefix:"KAFKA_"`
}

// WorkerConfig is everything order's own consumer of its order events reads
// from the environment. It calls inventory — the same service Checkout itself
// calls synchronously, but triggered by an event instead of a request — and
// needs neither the gRPC server's port nor catalog, which only Checkout calls.
type WorkerConfig struct {
	Log logger.Config        `envPrefix:"LOG_"`
	Obs observability.Config `envPrefix:"OBS_"`
	DB  postgres.Config      `envPrefix:"ORDER_DB_"`

	Inventory client.Config `envPrefix:"ORDER_INVENTORY_"`

	// Kafka is also the producer settings for this process's own DLQ writes:
	// a dead letter is a message like any other, published with the same
	// guarantee the outbox relay publishes with.
	Kafka kafkax.PublisherConfig `envPrefix:"KAFKA_"`

	// Two subscriptions, because a consumer reads one topic. Separate group
	// ids as well as separate topics: the group is half the key in
	// processed_events, so one shared id would let a delivery from one topic
	// mask a delivery from the other that happened to carry the same event id.
	Consumer        kafkax.ConsumerConfig `envPrefix:"KAFKA_CONSUMER_"`
	PaymentConsumer kafkax.ConsumerConfig `envPrefix:"KAFKA_PAYMENT_CONSUMER_"`
}
