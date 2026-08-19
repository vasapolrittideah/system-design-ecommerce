package grpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	inventoryv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/inventory/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/port/in"
)

// GetReservation reads one hold with its lines.
func (h *InventoryHandler) GetReservation(
	ctx context.Context,
	req *inventoryv1.GetReservationRequest,
) (*inventoryv1.GetReservationResponse, error) {
	reservation, err := h.inventory.GetReservation(ctx, req.GetId())
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &inventoryv1.GetReservationResponse{Reservation: toReservationProto(reservation)}, nil
}

// ReserveStock holds stock for an order.
func (h *InventoryHandler) ReserveStock(
	ctx context.Context,
	req *inventoryv1.ReserveStockRequest,
) (*inventoryv1.ReserveStockResponse, error) {
	reservation, err := h.inventory.ReserveStock(ctx, in.ReserveStockCommand{
		OrderID: req.GetOrderId(),
		Lines:   fromProtoLines(req.GetLines()),
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &inventoryv1.ReserveStockResponse{Reservation: toReservationProto(reservation)}, nil
}

// CommitReservation turns a hold into a sale.
func (h *InventoryHandler) CommitReservation(
	ctx context.Context,
	req *inventoryv1.CommitReservationRequest,
) (*inventoryv1.CommitReservationResponse, error) {
	reservation, err := h.inventory.CommitReservation(ctx, req.GetId())
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &inventoryv1.CommitReservationResponse{Reservation: toReservationProto(reservation)}, nil
}

// ReleaseReservation gives the held stock back.
func (h *InventoryHandler) ReleaseReservation(
	ctx context.Context,
	req *inventoryv1.ReleaseReservationRequest,
) (*inventoryv1.ReleaseReservationResponse, error) {
	reservation, err := h.inventory.ReleaseReservation(ctx, req.GetId())
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &inventoryv1.ReleaseReservationResponse{Reservation: toReservationProto(reservation)}, nil
}

// fromProtoLines maps the basket a reserve carries. Repeats of one SKU are
// passed through as they arrived: summing them is the aggregate's, and doing it
// here would be a second place holding the same rule.
func fromProtoLines(lines []*inventoryv1.NewReservationLine) []in.NewLine {
	commands := make([]in.NewLine, 0, len(lines))
	for _, line := range lines {
		commands = append(commands, in.NewLine{SKU: line.GetSku(), Quantity: line.GetQuantity()})
	}

	return commands
}

// toReservationProto maps the aggregate to what this service tells everyone
// else. The optimistic-locking version is dropped: it is not a fact anyone
// outside should be able to depend on.
func toReservationProto(reservation *domain.Reservation) *inventoryv1.Reservation {
	lines := make([]*inventoryv1.ReservationLine, 0, len(reservation.Lines()))

	for _, line := range reservation.Lines() {
		lines = append(lines, &inventoryv1.ReservationLine{
			Sku:      line.SKU.String(),
			Quantity: line.Quantity.Int32(),
		})
	}

	return &inventoryv1.Reservation{
		Id:        reservation.ID().String(),
		OrderId:   reservation.OrderID().String(),
		Lines:     lines,
		Status:    toProtoStatus(reservation.Status()),
		ExpiresAt: timestamppb.New(reservation.ExpiresAt()),
		CreatedAt: timestamppb.New(reservation.CreatedAt()),
		UpdatedAt: timestamppb.New(reservation.UpdatedAt()),
	}
}

// toProtoStatus maps the lifecycle state outward.
func toProtoStatus(status domain.ReservationStatus) inventoryv1.ReservationStatus {
	switch status {
	case domain.StatusHeld:
		return inventoryv1.ReservationStatus_RESERVATION_STATUS_HELD
	case domain.StatusCommitted:
		return inventoryv1.ReservationStatus_RESERVATION_STATUS_COMMITTED
	case domain.StatusReleased:
		return inventoryv1.ReservationStatus_RESERVATION_STATUS_RELEASED
	case domain.StatusExpired:
		return inventoryv1.ReservationStatus_RESERVATION_STATUS_EXPIRED
	default:
		// A row stored before a state was named, which Reconstitute loads rather
		// than refuses. Unspecified is the honest answer: inventing one of the
		// four would tell a saga its stock is held when it may not be.
		return inventoryv1.ReservationStatus_RESERVATION_STATUS_UNSPECIFIED
	}
}
