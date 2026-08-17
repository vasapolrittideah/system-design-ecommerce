package bootstrap

import (
	"github.com/jackc/pgx/v5/pgxpool"

	catalogv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/catalog/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/adapter/in/grpc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/adapter/out/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/catalog/internal/app"
)

// NewCatalogHandler assembles the service and returns the gRPC server to
// register.
//
// This is the one place that knows the concrete types, which is what lets every
// other package name only its ports — and what makes replacing PostgreSQL a
// change to these few lines.
//
// It returns the generated server interface rather than *grpc.CatalogHandler so
// that a caller cannot reach past the contract into the adapter.
func NewCatalogHandler(pool *pgxpool.Pool) catalogv1.CatalogServiceServer {
	products := postgres.NewProductRepository(pool)
	tx := txmanager.New(pool)

	return grpc.NewCatalogHandler(app.NewProductService(products, tx))
}
