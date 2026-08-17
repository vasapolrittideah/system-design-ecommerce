package bootstrap

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	catalogv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/catalog/v1"
	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/auth"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/httpx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/bff-web/internal/adapter/in/rest"
)

// Conns are the connections this BFF fans out over, one per service it calls.
//
// They are named rather than positional because they are interchangeable to the
// compiler and not at all to the process: two *grpc.ClientConn swapped at the
// call site would send every login to catalog, and nothing but a failing request
// would say so.
type Conns struct {
	Identity *grpc.ClientConn
	Catalog  *grpc.ClientConn
}

// NewRouter assembles the API and returns the handler to serve.
//
// This is the one place that knows the concrete types, which is what lets the
// adapter name only the generated client interfaces — and what makes pointing
// this BFF at another service a change to these few lines.
//
// MustNewVerifier panics on a key it cannot parse, which is the behaviour this
// process wants: a BFF that cannot verify a token would answer 401 to every
// authenticated request, and failing here fails the rollout instead.
func NewRouter(cfg Config, conns Conns, log *zap.Logger, reg prometheus.Registerer) http.Handler {
	validator := httpx.MustNewValidator()

	router := httpx.MustNewRouter(validator,
		httpx.WithRouterLogger(log),
		httpx.WithRouterMetrics(reg),
		httpx.WithRequestTimeout(cfg.RequestTimeout),
	)

	handler := rest.NewHandler(
		identityv1.NewIdentityServiceClient(conns.Identity),
		catalogv1.NewCatalogServiceClient(conns.Catalog),
		validator,
	)
	handler.Mount(router, auth.Authenticate(auth.MustNewVerifier(cfg.JWT)))

	return router
}
