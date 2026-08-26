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

// Consumers are the two subscriptions cmd/worker runs, each handed to a
// kafkax.Consumer of its own because a consumer reads one topic.
//
// They are two views onto one saga rather than two sagas: the same
// CheckoutSagaService is behind both, which is what keeps the whole sequence
// readable in one file instead of spread across the processes that trigger it.
type Consumers struct {
	// OrderEvents is this service reading back what it published, to make the
	// call to inventory that an order's own outcome triggers.
	OrderEvents *kafkaadapter.OrderEventsConsumer

	// PaymentEvents is the payment service telling this one what became of the
	// money, which is what moves the order's state machine.
	PaymentEvents *kafkaadapter.PaymentEventsConsumer
}

// NewCheckoutSagaConsumers assembles both of this service's subscriptions.
//
// It dials no catalog connection: unlike NewOrderHandler, nothing behind these
// paths prices a cart.
func NewCheckoutSagaConsumers(pool *pgxpool.Pool, inventoryConn *grpc.ClientConn) Consumers {
	orders := postgres.NewOrderRepository(pool)
	inboxStore := postgres.NewInboxStore(pool)
	tx := txmanager.New(pool)

	inventory := grpcclient.NewInventoryGateway(inventoryv1.NewInventoryServiceClient(inventoryConn))

	saga := app.NewCheckoutSagaService(orders, inboxStore, inventory, tx)

	return Consumers{
		OrderEvents:   kafkaadapter.NewOrderEventsConsumer(saga),
		PaymentEvents: kafkaadapter.NewPaymentEventsConsumer(saga),
	}
}
