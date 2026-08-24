package bootstrap

import (
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/kafkax"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/observability"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/outbox"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres"
)

// RelayConfig is everything the outbox relay reads from the environment.
//
// It is a struct of its own rather than the server's, though both processes
// mount the same ConfigMap and the same Secret. The relay moves rows to a
// broker and signs nothing, so a signing key it cannot use must not be a key it
// fails to start without — and the day identity's own key is rotated, a relay
// that parsed it would restart for a value it never reads.
type RelayConfig struct {
	Log logger.Config        `envPrefix:"LOG_"`
	Obs observability.Config `envPrefix:"OBS_"`
	DB  postgres.Config      `envPrefix:"IDENTITY_DB_"`

	Outbox outbox.RelayConfig     `envPrefix:"OUTBOX_"`
	Kafka  kafkax.PublisherConfig `envPrefix:"KAFKA_"`
}
