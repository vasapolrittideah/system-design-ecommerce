package bootstrap

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	orderv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/order/v1"
	paymentv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/payment/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	grpcadapter "github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/adapter/in/grpc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/adapter/out/grpcclient"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/adapter/out/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/adapter/out/provider"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/app"
)

// NewPaymentHandler assembles the service and returns the gRPC server to
// register.
//
// This is the one place that knows the concrete types, which is what lets every
// other package name only its ports — and what makes signing with a different
// provider a change to one line here plus a sibling of the provider adapter.
//
// It returns the generated server interface rather than *grpc.PaymentHandler so
// that a caller cannot reach past the contract into the adapter.
func NewPaymentHandler(
	pool *pgxpool.Pool,
	orderConn *grpc.ClientConn,
	providerCfg provider.Config,
) paymentv1.PaymentServiceServer {
	payments := postgres.NewPaymentRepository(pool)
	idem := postgres.NewIdempotencyStore(pool)
	tx := txmanager.New(pool)

	orders := grpcclient.NewOrderGateway(orderv1.NewOrderServiceClient(orderConn))
	gateway := provider.New(providerCfg)

	return grpcadapter.NewPaymentHandler(app.NewPaymentService(payments, idem, orders, gateway, tx))
}
