// Package bootstrap assembles the inventory service: it owns the configuration
// struct and the dependency wiring, so cmd/ stays a thin entry point and the
// order things are constructed in is decided in one reviewable place.
package bootstrap

import (
	"time"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx/server"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/observability"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/postgres"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/adapter/in/reaper"
)

// Config is everything this process reads from the environment.
//
// One struct with nested prefixes rather than one config.Load per dependency,
// because pkg/config reports every parse failure together: a deployment missing
// three variables says so once, instead of revealing them one restart at a time.
//
// Both binaries load the same struct. The server never reads Reaper and the
// reaper never reads GRPC, and that is cheaper than two structs that have to
// agree about the database: they run from one image against one database, and a
// value that differs between them is a value one of them is wrong about.
//
// A variable that matches no field here is ignored rather than rejected, so
// check the struct before adding one to deploy/k8s/base/inventory/config.env.
//
// There is no signing key and no token policy: this service is reached over
// east-west gRPC with identity already settled a hop earlier, and the only
// question it answers about a caller is whether there is one left.
type Config struct {
	Log  logger.Config        `envPrefix:"LOG_"`
	Obs  observability.Config `envPrefix:"OBS_"`
	GRPC server.Config        `envPrefix:"INVENTORY_GRPC_"`
	DB   postgres.Config      `envPrefix:"INVENTORY_DB_"`

	Reservation ReservationConfig `envPrefix:"INVENTORY_RESERVATION_"`
	Reaper      reaper.Config     `envPrefix:"INVENTORY_REAPER_"`
}

// ReservationConfig is how long this service holds stock for an order.
type ReservationConfig struct {
	// TTL has to exceed the longest a checkout can legitimately take — a
	// payment provider redirect is minutes, not seconds — because a hold that
	// expires under a shopper who is still paying becomes a refund. It also has
	// to be short enough that an abandoned basket does not keep a popular SKU
	// off the shelf for an afternoon.
	TTL time.Duration `env:"TTL" envDefault:"15m"`
}
