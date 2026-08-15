package bootstrap

import (
	"github.com/jackc/pgx/v5/pgxpool"

	identityv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/identity/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/in/grpc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/out/argon2"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/adapter/out/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/identity/internal/app"
)

// NewIdentityHandler assembles the user slice and returns the gRPC service to
// register.
//
// This is the one place that knows the concrete types, which is what lets every
// other package name only its ports — and what makes replacing argon2 or
// PostgreSQL a change to these four lines.
//
// It returns the generated server interface rather than *grpc.UserHandler so
// that a caller cannot reach past the contract into the adapter.
func NewIdentityHandler(pool *pgxpool.Pool) identityv1.IdentityServiceServer {
	users := postgres.NewUserRepository(pool)
	hasher := argon2.NewHasher()

	return grpc.NewUserHandler(app.NewUserService(users, hasher))
}
