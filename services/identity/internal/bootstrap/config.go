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
// three variables says so once, instead of revealing them one restart at a
// time. Loading each block separately would give up exactly that.
//
// The prefixes are the ones each pkg/ config documents. A name that does not
// match a field is ignored rather than rejected, so check the struct before
// adding a variable to deploy/k8s/base/identity/config.env.
type Config struct {
	Log  logger.Config        `envPrefix:"LOG_"`
	Obs  observability.Config `envPrefix:"OBS_"`
	GRPC server.Config        `envPrefix:"IDENTITY_GRPC_"`
	DB   postgres.Config      `envPrefix:"IDENTITY_DB_"`

	// IDENTITY_JWT_* is deliberately absent. The manifests already carry it and
	// the Secret is already mounted, but this slice mints no tokens, and a
	// signer constructed here would be an unused value that only appears to
	// have been validated. It arrives with Login, where parsing the key at
	// startup is what turns a bad key into a failed rollout rather than a
	// failed sign-in.
}
