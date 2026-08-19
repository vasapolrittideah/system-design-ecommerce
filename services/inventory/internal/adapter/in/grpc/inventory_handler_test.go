package grpc_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	inventoryv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/inventory/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/adapter/in/grpc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/port/in"
)

const orderID = "6f1c0a4e-2b8d-4c1a-9f3e-5d7b8a2c4e60"

var now = time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)

// stubUseCase records what the handler asked for and answers with what the test
// wants. Hand-written rather than generated because only driven ports are listed
// in .mockery.yml, and what these tests assert on is the mapping.
type stubUseCase struct {
	create  in.CreateStockItemCommand
	adjust  in.AdjustStockCommand
	reserve in.ReserveStockCommand
	id      string
	skus    []string

	item        *domain.StockItem
	items       []*domain.StockItem
	reservation *domain.Reservation
	err         error
}

func (s *stubUseCase) GetStockBySKUs(_ context.Context, skus []string) ([]*domain.StockItem, error) {
	s.skus = skus

	return s.items, s.err
}

func (s *stubUseCase) GetReservation(_ context.Context, id string) (*domain.Reservation, error) {
	s.id = id

	return s.reservation, s.err
}

func (s *stubUseCase) CreateStockItem(
	_ context.Context,
	cmd in.CreateStockItemCommand,
) (*domain.StockItem, error) {
	s.create = cmd

	return s.item, s.err
}

func (s *stubUseCase) AdjustStock(_ context.Context, cmd in.AdjustStockCommand) (*domain.StockItem, error) {
	s.adjust = cmd

	return s.item, s.err
}

func (s *stubUseCase) ReserveStock(
	_ context.Context,
	cmd in.ReserveStockCommand,
) (*domain.Reservation, error) {
	s.reserve = cmd

	return s.reservation, s.err
}

func (s *stubUseCase) CommitReservation(_ context.Context, id string) (*domain.Reservation, error) {
	s.id = id

	return s.reservation, s.err
}

func (s *stubUseCase) ReleaseReservation(_ context.Context, id string) (*domain.Reservation, error) {
	s.id = id

	return s.reservation, s.err
}

func setup(stub *stubUseCase) *adapter.InventoryHandler {
	return adapter.NewInventoryHandler(stub)
}

func heldReservation(t *testing.T) *domain.Reservation {
	t.Helper()

	reservation, err := domain.NewReservation(
		domain.OrderID(orderID),
		[]domain.Line{{SKU: "SHIRT-M", Quantity: 2}},
		now,
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("NewReservation() error = %v, want nil", err)
	}

	return reservation
}

func TestGetStockBySKUs(t *testing.T) {
	stub := &stubUseCase{items: []*domain.StockItem{domain.NewStockItem("SHIRT-M", 5)}}

	res, err := setup(stub).GetStockBySKUs(context.Background(), &inventoryv1.GetStockBySKUsRequest{
		Skus: []string{"SHIRT-M"},
	})
	if err != nil {
		t.Fatalf("GetStockBySKUs() error = %v, want nil", err)
	}

	if len(stub.skus) != 1 || stub.skus[0] != "SHIRT-M" {
		t.Errorf("use case got skus %v, want [SHIRT-M]", stub.skus)
	}

	if len(res.GetItems()) != 1 {
		t.Fatalf("GetStockBySKUs() = %d items, want 1", len(res.GetItems()))
	}

	item := res.GetItems()[0]
	if item.GetSku() != "SHIRT-M" || item.GetAvailable() != 5 {
		t.Errorf("item = %s/%d, want SHIRT-M/5", item.GetSku(), item.GetAvailable())
	}
}

func TestReserveStock(t *testing.T) {
	stub := &stubUseCase{reservation: heldReservation(t)}

	res, err := setup(stub).ReserveStock(context.Background(), &inventoryv1.ReserveStockRequest{
		OrderId: orderID,
		Lines: []*inventoryv1.NewReservationLine{
			{Sku: "SHIRT-M", Quantity: 1},
			{Sku: "SHIRT-M", Quantity: 1},
		},
	})
	if err != nil {
		t.Fatalf("ReserveStock() error = %v, want nil", err)
	}

	if stub.reserve.OrderID != orderID {
		t.Errorf("use case got order %q, want %q", stub.reserve.OrderID, orderID)
	}

	// Repeats are passed through as they arrived. Summing them is the
	// aggregate's, and doing it here would be a second place holding the rule.
	if len(stub.reserve.Lines) != 2 {
		t.Errorf("use case got %d lines, want the 2 that were sent", len(stub.reserve.Lines))
	}

	reservation := res.GetReservation()
	if got := reservation.GetStatus(); got != inventoryv1.ReservationStatus_RESERVATION_STATUS_HELD {
		t.Errorf("status = %v, want HELD", got)
	}

	if got := reservation.GetExpiresAt().AsTime(); !got.Equal(now.Add(15 * time.Minute)) {
		t.Errorf("expires_at = %v, want %v", got, now.Add(15*time.Minute))
	}

	if got := reservation.GetLines(); len(got) != 1 || got[0].GetQuantity() != 2 {
		t.Errorf("lines = %v, want one line of 2", got)
	}
}

func TestStatusMapping(t *testing.T) {
	tests := []struct {
		status domain.ReservationStatus
		want   inventoryv1.ReservationStatus
	}{
		{status: domain.StatusHeld, want: inventoryv1.ReservationStatus_RESERVATION_STATUS_HELD},
		{status: domain.StatusCommitted, want: inventoryv1.ReservationStatus_RESERVATION_STATUS_COMMITTED},
		{status: domain.StatusReleased, want: inventoryv1.ReservationStatus_RESERVATION_STATUS_RELEASED},
		{status: domain.StatusExpired, want: inventoryv1.ReservationStatus_RESERVATION_STATUS_EXPIRED},
		// A row stored before a state was named. Inventing one of the four would
		// tell a saga its stock is held when it may not be.
		{status: "invented-later", want: inventoryv1.ReservationStatus_RESERVATION_STATUS_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			stub := &stubUseCase{reservation: domain.ReconstituteReservation(domain.ReservationSnapshot{
				ID:      domain.NewReservationID(),
				OrderID: domain.OrderID(orderID),
				Lines:   []domain.Line{{SKU: "SHIRT-M", Quantity: 1}},
				Status:  tt.status,
			})}

			res, err := setup(stub).GetReservation(context.Background(), &inventoryv1.GetReservationRequest{
				Id: "11111111-2222-3333-4444-555555555555",
			})
			if err != nil {
				t.Fatalf("GetReservation() error = %v, want nil", err)
			}

			if got := res.GetReservation().GetStatus(); got != tt.want {
				t.Errorf("status = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestErrorsLeaveThroughErrorx(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{
			name: "out of stock",
			err: errorx.Wrap(domain.ErrInsufficientStock, errorx.KindConflict, "no").
				WithReason("OUT_OF_STOCK").
				WithMetadata(map[string]string{"sku": "SHIRT-M"}),
			// FailedPrecondition rather than one of the codes a caller's circuit
			// breaker counts: a run of sold-out SKUs is this service working.
			want: codes.FailedPrecondition,
		},
		{
			name: "untracked sku",
			err:  errorx.Wrap(domain.ErrStockItemNotFound, errorx.KindNotFound, "no"),
			want: codes.NotFound,
		},
		{
			name: "a malformed quantity",
			err:  domain.ValidationError{Field: "quantity", Message: "must be at least 1"},
			want: codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubUseCase{err: tt.err}

			_, err := setup(stub).ReserveStock(context.Background(), &inventoryv1.ReserveStockRequest{
				OrderId: orderID,
				Lines:   []*inventoryv1.NewReservationLine{{Sku: "SHIRT-M", Quantity: 1}},
			})

			if got := status.Code(err); got != tt.want {
				t.Fatalf("ReserveStock() code = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("the reason code and metadata reach the client", func(t *testing.T) {
		// Reason codes are API: a client branches on them rather than on the
		// status, because FailedPrecondition alone cannot say whether a SKU was
		// sold out or a hold had already been committed.
		stub := &stubUseCase{err: errorx.Wrap(domain.ErrInsufficientStock, errorx.KindConflict, "no").
			WithReason("OUT_OF_STOCK").
			WithMetadata(map[string]string{"sku": "SHIRT-M"})}

		_, err := setup(stub).ReserveStock(context.Background(), &inventoryv1.ReserveStockRequest{
			OrderId: orderID,
			Lines:   []*inventoryv1.NewReservationLine{{Sku: "SHIRT-M", Quantity: 1}},
		})

		if got := errorx.Reason(err); got != "OUT_OF_STOCK" {
			t.Errorf("Reason() = %q, want OUT_OF_STOCK", got)
		}

		if got := errorx.Metadata(err)["sku"]; got != "SHIRT-M" {
			t.Errorf("Metadata()[sku] = %q, want SHIRT-M", got)
		}
	})
}

func TestAdjustStockMapsTheCommand(t *testing.T) {
	stub := &stubUseCase{item: domain.NewStockItem("SHIRT-M", 5)}

	_, err := setup(stub).AdjustStock(context.Background(), &inventoryv1.AdjustStockRequest{
		Sku:    "SHIRT-M",
		Delta:  -2,
		Reason: "two broke in transit",
	})
	if err != nil {
		t.Fatalf("AdjustStock() error = %v, want nil", err)
	}

	want := in.AdjustStockCommand{SKU: "SHIRT-M", Delta: -2, Reason: "two broke in transit"}
	if stub.adjust != want {
		t.Errorf("use case got %+v, want %+v", stub.adjust, want)
	}
}
