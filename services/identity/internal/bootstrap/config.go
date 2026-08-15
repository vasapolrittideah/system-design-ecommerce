// Package bootstrap assembles the identity service: it owns the configuration
// struct and the dependency wiring, so cmd/ stays a thin entry point and the
// order things are constructed in is decided in one reviewable place.
package bootstrap

import (
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/server"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/observability"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres"
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

	// IDENTITY_JWT_* is deliberately absent: the manifests carry it and the
	// Secret is mounted, but this slice mints no tokens, and a signer
	// constructed here would only appear to have been validated. It arrives
	// with Login, where parsing the key at startup turns a bad key into a
	// failed rollout rather than a failed sign-in.
}
