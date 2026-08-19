package grpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	inventoryv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/inventory/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/port/in"
)

// GetStockBySKUs reads the counts for many SKUs, returning only the ones this
// service tracks.
//
// There is no authorization check, and that is a decision rather than an
// omission: this RPC is reached over east-west gRPC, and whether a caller may
// ask about a SKU is settled by which services can reach this port at all.
func (h *InventoryHandler) GetStockBySKUs(
	ctx context.Context,
	req *inventoryv1.GetStockBySKUsRequest,
) (*inventoryv1.GetStockBySKUsResponse, error) {
	items, err := h.inventory.GetStockBySKUs(ctx, req.GetSkus())
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &inventoryv1.GetStockBySKUsResponse{Items: toStockProtos(items)}, nil
}

// CreateStockItem starts tracking a SKU.
func (h *InventoryHandler) CreateStockItem(
	ctx context.Context,
	req *inventoryv1.CreateStockItemRequest,
) (*inventoryv1.CreateStockItemResponse, error) {
	item, err := h.inventory.CreateStockItem(ctx, in.CreateStockItemCommand{
		SKU:       req.GetSku(),
		Available: req.GetAvailable(),
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &inventoryv1.CreateStockItemResponse{Item: toStockProto(item)}, nil
}

// AdjustStock moves the available count by a delta.
func (h *InventoryHandler) AdjustStock(
	ctx context.Context,
	req *inventoryv1.AdjustStockRequest,
) (*inventoryv1.AdjustStockResponse, error) {
	item, err := h.inventory.AdjustStock(ctx, in.AdjustStockCommand{
		SKU:    req.GetSku(),
		Delta:  req.GetDelta(),
		Reason: req.GetReason(),
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &inventoryv1.AdjustStockResponse{Item: toStockProto(item)}, nil
}

// toStockProtos maps a batch of counts.
func toStockProtos(items []*domain.StockItem) []*inventoryv1.StockItem {
	messages := make([]*inventoryv1.StockItem, 0, len(items))
	for _, item := range items {
		messages = append(messages, toStockProto(item))
	}

	return messages
}

// toStockProto maps the counts to what this service tells everyone else.
//
// The row's uuid is dropped here, deliberately: a field on
// ecommerce.inventory.v1.StockItem is a promise to every caller, and the SKU is
// the only key anything outside this service has a reason to hold.
func toStockProto(item *domain.StockItem) *inventoryv1.StockItem {
	return &inventoryv1.StockItem{
		Sku:       item.SKU().String(),
		Available: item.Available().Int32(),
		Reserved:  item.Reserved().Int32(),
		CreatedAt: timestamppb.New(item.CreatedAt()),
		UpdatedAt: timestamppb.New(item.UpdatedAt()),
	}
}
