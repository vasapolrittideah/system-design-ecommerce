// Package bootstrap assembles the identity service: it owns the configuration
// structs and the dependency wiring, so cmd/ stays a thin entry point and the
// order things are constructed in is decided in one reviewable place.
package bootstrap

import (
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/auth"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/server"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/kafkax"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/observability"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/outbox"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/app"
)

// Config is everything this process reads from the environment.
//
// One struct with nested prefixes rather than one config.Load per dependency,
// because pkg/config reports every parse failure together: a deployment missing
// three variables says so once, instead of revealing them one restart at a time.
//
// A variable that matches no field here is ignored rather than rejected, so
// check the struct before adding one to deploy/k8s/base/identity/config.env.
type Config struct {
	Log  logger.Config        `envPrefix:"LOG_"`
	Obs  observability.Config `envPrefix:"OBS_"`
	GRPC server.Config        `envPrefix:"IDENTITY_GRPC_"`
	DB   postgres.Config      `envPrefix:"IDENTITY_DB_"`

	// The private key is loaded here, at startup, rather than where a token is
	// first signed: parsing it now turns a bad or missing key into a failed
	// rollout, and parsing it lazily turns the same key into a sign-in that
	// fails once the pod is already serving.
	JWT auth.SignerConfig `envPrefix:"IDENTITY_JWT_"`

	Session app.SessionConfig `envPrefix:"IDENTITY_SESSION_"`
}

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
