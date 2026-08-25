package bootstrap

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	catalogv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/catalog/v1"
	inventoryv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/inventory/v1"
	orderv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/order/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	grpcadapter "github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/adapter/in/grpc"
	kafkaadapter "github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/adapter/in/kafka"
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

	return grpcadapter.NewOrderHandler(app.NewOrderService(orders, idem, inventory, catalog, tx))
}

// NewCheckoutSagaConsumer assembles this service's own consumer of its order
// events and returns the handler cmd/worker hands to a kafkax.Consumer.
//
// It dials no catalog connection: unlike NewOrderHandler, nothing behind this
// path prices a cart.
func NewCheckoutSagaConsumer(pool *pgxpool.Pool, inventoryConn *grpc.ClientConn) *kafkaadapter.OrderEventsConsumer {
	inboxStore := postgres.NewInboxStore(pool)
	tx := txmanager.New(pool)

	inventory := grpcclient.NewInventoryGateway(inventoryv1.NewInventoryServiceClient(inventoryConn))

	saga := app.NewCheckoutSagaService(inboxStore, inventory, tx)

	return kafkaadapter.NewOrderEventsConsumer(saga)
}
