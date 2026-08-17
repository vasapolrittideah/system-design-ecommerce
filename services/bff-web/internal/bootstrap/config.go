// Package bootstrap assembles the web BFF: it owns the configuration struct and
// the dependency wiring, so cmd/ stays a thin entry point and the order things
// are constructed in is decided in one reviewable place.
package bootstrap

import (
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/auth"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/client"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/observability"
)

// Config is everything this process reads from the environment.
//
// One struct with nested prefixes rather than one config.Load per dependency,
// because pkg/config reports every parse failure together: a deployment missing
// three variables says so once, instead of revealing them one restart at a time.
//
// A variable that matches no field here is ignored rather than rejected, so
// check the struct before adding one to deploy/k8s/base/bff-web/config.env.
type Config struct {
	Log  logger.Config        `envPrefix:"LOG_"`
	Obs  observability.Config `envPrefix:"OBS_"`
	HTTP httpx.ServerConfig   `envPrefix:"BFF_WEB_HTTP_"`

	// The public key is parsed here, at startup, rather than on the first
	// request that carries a token: a malformed key becomes a failed rollout
	// instead of an API that returns 401 to everyone once it is already serving.
	//
	// It stays a plain string where the signer's private key is a config.Secret.
	// A verification key cannot mint anything, and seeing it in a log line is
	// how a key mismatch between this process and identity gets diagnosed.
	JWT auth.VerifierConfig `envPrefix:"BFF_WEB_JWT_"`

	// One block per service this BFF calls, each configured independently: a
	// listing read and a login are not the same call, and the timeout, retry,
	// and breaker settings that suit one are not the ones that suit the other.
	Identity client.Config `envPrefix:"BFF_WEB_IDENTITY_"`
	Catalog  client.Config `envPrefix:"BFF_WEB_CATALOG_"`

	// RequestTimeout is this tier's share of the cascading budget — Kong 5s,
	// here, downstream 300ms — and it bounds every fan-out call a request makes,
	// because pkg/grpcx/client inherits the deadline from the context.
	//
	// The gap between this and the downstream timeout is deliberate headroom: it
	// is what a fallback, a second attempt at an optional dependency, or the
	// assembly of a partial response gets to spend after a call has already
	// failed.
	RequestTimeout time.Duration `env:"BFF_WEB_REQUEST_TIMEOUT" envDefault:"800ms"`
}
