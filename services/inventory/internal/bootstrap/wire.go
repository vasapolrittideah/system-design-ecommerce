package bootstrap

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	inventoryv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/inventory/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/adapter/in/grpc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/adapter/in/reaper"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/adapter/out/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/app"
)

// NewInventoryHandler assembles the service and returns the gRPC server to
// register.
//
// This is the one place that knows the concrete types, which is what lets every
// other package name only its ports — and what makes replacing PostgreSQL a
// change to these few lines.
//
// It returns the generated server interface rather than *grpc.InventoryHandler
// so that a caller cannot reach past the contract into the adapter.
func NewInventoryHandler(pool *pgxpool.Pool, cfg Config) inventoryv1.InventoryServiceServer {
	return grpc.NewInventoryHandler(newInventoryService(pool, cfg))
}

// NewReaper assembles the sweep and its timer.
//
// A second entry point rather than a second wiring: the reaper drives the same
// use cases against the same database as the server, and the only thing it does
// differently is who calls.
func NewReaper(pool *pgxpool.Pool, cfg Config, reg prometheus.Registerer) (*reaper.Reaper, error) {
	return reaper.New(newInventoryService(pool, cfg), cfg.Reaper, reaper.WithRegisterer(reg))
}

// newInventoryService builds the use cases over their driven ports.
func newInventoryService(pool *pgxpool.Pool, cfg Config) *app.InventoryService {
	stock := postgres.NewStockRepository(pool)
	reservations := postgres.NewReservationRepository(pool)
	tx := txmanager.New(pool)

	return app.NewInventoryService(stock, reservations, tx, app.Policy{
		ReservationTTL: cfg.Reservation.TTL,
		Now:            time.Now,
	})
}
