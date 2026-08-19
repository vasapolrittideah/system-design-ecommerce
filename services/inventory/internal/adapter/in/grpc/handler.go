// Package grpc is the driving adapter: it maps ecommerce.inventory.v1 messages
// onto use case commands and back, and nothing else.
//
// Two absences are deliberate. It validates nothing, because the constraints are
// declared in the proto and enforced by an interceptor; and it constructs no
// errors, because every failure arrives already classified and leaves through
// errorx.ToGRPC.
package grpc

import (
	inventoryv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/inventory/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/port/in"
)

// InventoryHandler serves InventoryService.
//
// Embedding the generated Unimplemented struct lets a new RPC be added to the
// proto without breaking the build here: the method answers Unimplemented until
// someone writes it.
//
// It holds the driving port for requests and not the reaper, which has no RPC on
// purpose — sweeping the warehouse is a timer's job in another process, and a
// handler that could do it would be one accidental route away from a client
// doing it.
type InventoryHandler struct {
	inventoryv1.UnimplementedInventoryServiceServer

	inventory in.InventoryUseCase
}

var _ inventoryv1.InventoryServiceServer = (*InventoryHandler)(nil)

// NewInventoryHandler builds the handler over the driving port.
func NewInventoryHandler(inventory in.InventoryUseCase) *InventoryHandler {
	return &InventoryHandler{inventory: inventory}
}
