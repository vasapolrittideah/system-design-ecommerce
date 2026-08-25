package bootstrap

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	catalogv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/catalog/v1"
	inventoryv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/inventory/v1"
	orderv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/order/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/adapter/in/grpc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/adapter/out/grpcclient"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/adapter/out/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/app"
)

// Conns are the connections checkout fans out over, one per service it calls.
//
// They are named rather than positional because they are interchangeable to the
// compiler and not at all to the process: two *grpc.ClientConn swapped at the
// call site would ask the warehouse for prices, and nothing but a failing
// checkout would say so.
type Conns struct {
	Inventory *grpc.ClientConn
	Catalog   *grpc.ClientConn
}

// NewOrderHandler assembles the service and returns the gRPC server to register.
//
// This is the one place that knows the concrete types, which is what lets every
// other package name only its ports — and what makes replacing PostgreSQL, or
// pointing checkout at another warehouse, a change to these few lines.
//
// It returns the generated server interface rather than *grpc.OrderHandler so
// that a caller cannot reach past the contract into the adapter.
func NewOrderHandler(pool *pgxpool.Pool, conns Conns) orderv1.OrderServiceServer {
	orders := postgres.NewOrderRepository(pool)
	idem := postgres.NewIdempotencyStore(pool)
	tx := txmanager.New(pool)

	inventory := grpcclient.NewInventoryGateway(inventoryv1.NewInventoryServiceClient(conns.Inventory))
	catalog := grpcclient.NewCatalogGateway(catalogv1.NewCatalogServiceClient(conns.Catalog))

	return adapter.NewOrderHandler(app.NewOrderService(orders, idem, inventory, catalog, tx))
}
