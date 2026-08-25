// Package grpcclient is the driven adapter for the services this one calls: it
// maps between the domain's vocabulary and the generated clients, and lets the
// failures the far end classified travel back unflattened.
//
// It is the only package here that knows another service exists.
package grpcclient

import (
	"context"

	inventoryv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/inventory/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out"
)

// InventoryGateway calls the inventory service.
type InventoryGateway struct {
	client inventoryv1.InventoryServiceClient
}

var _ out.InventoryGateway = (*InventoryGateway)(nil)

// NewInventoryGateway builds the gateway over a generated client.
func NewInventoryGateway(client inventoryv1.InventoryServiceClient) *InventoryGateway {
	return &InventoryGateway{client: client}
}

// Reserve holds stock for the order.
//
// The error is returned as it arrived. A sold-out SKU comes back as
// FailedPrecondition with OUT_OF_STOCK and the SKU in its metadata, and
// rewrapping it would cost the caller the one thing it needs to name the line
// that failed — errorx.ToGRPC keeps a status that is already a status for
// exactly this reason.
func (g *InventoryGateway) Reserve(
	ctx context.Context,
	orderID domain.OrderID,
	lines []out.ReservationLine,
) (domain.ReservationID, error) {
	requested := make([]*inventoryv1.NewReservationLine, 0, len(lines))
	for _, line := range lines {
		requested = append(requested, &inventoryv1.NewReservationLine{
			Sku:      line.SKU.String(),
			Quantity: narrow(line.Quantity),
		})
	}

	res, err := g.client.ReserveStock(ctx, &inventoryv1.ReserveStockRequest{
		OrderId: orderID.String(),
		Lines:   requested,
	})
	if err != nil {
		return "", err
	}

	return domain.ReservationID(res.GetReservation().GetId()), nil
}

// Release gives a hold back. It is idempotent at the far end, which is what
// makes it safe as a compensating step — compensation runs more than once.
func (g *InventoryGateway) Release(ctx context.Context, reservationID domain.ReservationID) error {
	_, err := g.client.ReleaseReservation(ctx, &inventoryv1.ReleaseReservationRequest{
		Id: reservationID.String(),
	})

	return err
}

// Commit turns the hold into a sale. Idempotent at the far end, so a
// redelivered OrderPaid calling this a second time changes nothing.
func (g *InventoryGateway) Commit(ctx context.Context, reservationID domain.ReservationID) error {
	_, err := g.client.CommitReservation(ctx, &inventoryv1.CommitReservationRequest{
		Id: reservationID.String(),
	})

	return err
}
