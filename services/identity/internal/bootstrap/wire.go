package bootstrap

import (
	"github.com/jackc/pgx/v5/pgxpool"

	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/auth"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/in/grpc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/out/argon2"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/out/jwt"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/out/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/app"
)

// NewIdentityHandler assembles the service and returns the gRPC server to
// register.
//
// This is the one place that knows the concrete types, which is what lets every
// other package name only its ports — and what makes replacing argon2,
// PostgreSQL, or the signer a change to these few lines.
//
// It returns the generated server interface rather than *grpc.IdentityHandler so
// that a caller cannot reach past the contract into the adapter.
//
// MustNewSigner panics on a key it cannot parse, which is the behaviour this
// process wants: a service that cannot mint tokens has nothing to serve, and
// failing here fails the rollout instead of every sign-in.
func NewIdentityHandler(cfg Config, pool *pgxpool.Pool) identityv1.IdentityServiceServer {
	users := postgres.NewUserRepository(pool)
	tokens := postgres.NewRefreshTokenRepository(pool)
	hasher := argon2.NewHasher()
	issuer := jwt.NewIssuer(auth.MustNewSigner(cfg.JWT))
	tx := txmanager.New(pool)

	return grpc.NewIdentityHandler(
		app.NewUserService(users, hasher),
		app.NewSessionService(cfg.Session, users, tokens, hasher, issuer, tx),
	)
}
