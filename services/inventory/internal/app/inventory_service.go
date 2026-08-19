// Package app holds the use cases: the orchestration between a request and the
// domain rules that answer it. There is deliberately very little here — an `if`
// in this package that encodes a business policy is a rule that escaped the
// domain, where it could have been tested without a mock.
//
// The `if`s that are here all read an answer the domain gave: whether a
// transition moved the reservation, and whether the hold an order already has is
// the one it is asking for.
package app

import (
	"context"
	"errors"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/inventory/internal/port/out"
)

// defaultSweepLimit is how many expired holds one pass of the reaper takes when
// its caller does not say. Small enough that the transaction holding the row
// locks is short, since every one of those rows is a SKU nobody else can reserve
// until it commits.
const defaultSweepLimit = 100

// InventoryService implements the inventory use cases over the two
// repositories.
type InventoryService struct {
	stock        out.StockRepository
	reservations out.ReservationRepository
	tx           out.TxManager

	policy Policy
}

// Compile-time proof that both driving ports are satisfied. Without it the
// failure surfaces in bootstrap, naming the wiring rather than the missing
// method.
var (
	_ in.InventoryUseCase  = (*InventoryService)(nil)
	_ in.ReservationReaper = (*InventoryService)(nil)
)

// NewInventoryService wires the use cases to their driven ports.
func NewInventoryService(
	stock out.StockRepository,
	reservations out.ReservationRepository,
	tx out.TxManager,
	policy Policy,
) *InventoryService {
	return &InventoryService{stock: stock, reservations: reservations, tx: tx, policy: policy}
}

// GetStockBySKUs reads the counts for many SKUs.
//
// One malformed SKU fails the whole call, where one *untracked* SKU does not:
// the first is a bad request, the second is the ordinary case of a screen naming
// something the warehouse has never held.
func (s *InventoryService) GetStockBySKUs(ctx context.Context, skus []string) ([]*domain.StockItem, error) {
	parsed := make([]domain.SKU, 0, len(skus))

	for _, sku := range skus {
		value, err := domain.NewSKU(sku)
		if err != nil {
			return nil, err
		}

		parsed = append(parsed, value)
	}

	return s.stock.FindBySKUs(ctx, parsed)
}

// GetReservation reads one hold with its lines.
func (s *InventoryService) GetReservation(ctx context.Context, id string) (*domain.Reservation, error) {
	reservationID, err := domain.ParseReservationID(id)
	if err != nil {
		return nil, err
	}

	return s.reservations.FindByID(ctx, reservationID)
}

// CreateStockItem starts tracking a SKU.
//
// No transaction: it is one statement, and the uniqueness it depends on is the
// constraint's rather than something read beforehand.
func (s *InventoryService) CreateStockItem(
	ctx context.Context,
	cmd in.CreateStockItemCommand,
) (*domain.StockItem, error) {
	sku, err := domain.NewSKU(cmd.SKU)
	if err != nil {
		return nil, err
	}

	available, err := domain.NewQuantity("available", cmd.Available)
	if err != nil {
		return nil, err
	}

	return s.stock.Create(ctx, domain.NewStockItem(sku, available))
}

// AdjustStock moves the available count by a delta.
func (s *InventoryService) AdjustStock(
	ctx context.Context,
	cmd in.AdjustStockCommand,
) (*domain.StockItem, error) {
	sku, err := domain.NewSKU(cmd.SKU)
	if err != nil {
		return nil, err
	}

	delta, err := domain.NewAdjustment(cmd.Delta)
	if err != nil {
		return nil, err
	}

	return s.stock.Adjust(ctx, sku, delta)
}

// ReserveStock holds stock for an order.
//
// The candidate reservation is built before the transaction opens, which is what
// lets a malformed basket be refused without costing a BEGIN — and what gives
// the idempotency check below something canonical to compare against, since the
// aggregate is where repeated SKUs are summed and sorted.
func (s *InventoryService) ReserveStock(
	ctx context.Context,
	cmd in.ReserveStockCommand,
) (*domain.Reservation, error) {
	orderID, err := domain.ParseOrderID(cmd.OrderID)
	if err != nil {
		return nil, err
	}

	lines := make([]domain.Line, 0, len(cmd.Lines))

	for _, line := range cmd.Lines {
		sku, err := domain.NewSKU(line.SKU)
		if err != nil {
			return nil, err
		}

		quantity, err := domain.NewQuantity("quantity", line.Quantity)
		if err != nil {
			return nil, err
		}

		lines = append(lines, domain.Line{SKU: sku, Quantity: quantity})
	}

	candidate, err := domain.NewReservation(orderID, lines, s.policy.Now(), s.policy.ReservationTTL)
	if err != nil {
		return nil, err
	}

	var held *domain.Reservation

	err = s.tx.Do(ctx, func(ctx context.Context) error {
		existing, err := s.reservations.FindHeldByOrderID(ctx, orderID)
		if err == nil {
			if !existing.Matches(candidate.Lines()) {
				return errorx.New(errorx.KindConflict, "order %s already holds a different reservation", orderID).
					WithReason("RESERVATION_LINES_DIFFER").
					WithMetadata(map[string]string{"reservation_id": existing.ID().String()})
			}

			held = existing

			return nil
		}

		if !errors.Is(err, domain.ErrReservationNotFound) {
			return err
		}

		// In the order the aggregate canonicalised them into, which is what
		// keeps two concurrent baskets sharing SKUs from deadlocking on each
		// other's row locks.
		for _, line := range candidate.Lines() {
			if err := s.stock.Reserve(ctx, line.SKU, line.Quantity); err != nil {
				return err
			}
		}

		held, err = s.reservations.Create(ctx, candidate)

		return err
	})
	if err != nil {
		return nil, err
	}

	return held, nil
}

// CommitReservation turns a hold into a sale.
func (s *InventoryService) CommitReservation(ctx context.Context, id string) (*domain.Reservation, error) {
	now := s.policy.Now()

	return s.settle(ctx, id,
		func(reservation *domain.Reservation) (bool, error) { return reservation.Commit(now) },
		s.stock.Commit,
	)
}

// ReleaseReservation gives the held stock back.
func (s *InventoryService) ReleaseReservation(ctx context.Context, id string) (*domain.Reservation, error) {
	return s.settle(ctx, id,
		func(reservation *domain.Reservation) (bool, error) { return reservation.Release() },
		s.stock.Release,
	)
}

// ExpireReservations sweeps holds nobody committed in time.
//
// One transaction for the whole batch, because the row locks ClaimExpired takes
// are what stop a second reaper working on the same reservations, and they last
// exactly as long as it.
func (s *InventoryService) ExpireReservations(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = defaultSweepLimit
	}

	// Read once, so every reservation in this pass is judged against the moment
	// the rows were selected on rather than against the clock drifting through
	// the loop.
	now := s.policy.Now()

	var swept int

	err := s.tx.Do(ctx, func(ctx context.Context) error {
		swept = 0

		claimed, err := s.reservations.ClaimExpired(ctx, now, limit)
		if err != nil {
			return err
		}

		for _, reservation := range claimed {
			moved, err := reservation.Expire(now)
			if err != nil {
				return err
			}

			if !moved {
				continue
			}

			for _, line := range reservation.Lines() {
				if err := s.stock.Release(ctx, line.SKU, line.Quantity); err != nil {
					return err
				}
			}

			if _, err := s.reservations.Update(ctx, reservation); err != nil {
				return err
			}

			swept++
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return swept, nil
}

// settle loads a reservation, asks its state machine to move, and applies the
// matching movement to every SKU it holds.
//
// Both halves are inside one transaction because the optimistic lock is only
// worth anything if they are: a version read on its own may already have moved
// by the time the update carrying it is sent, and a commit and a release racing
// would otherwise both take the stock.
//
// A transition that changed nothing skips the movement entirely, which is what
// makes committing or releasing twice safe rather than double-counting. That is
// the aggregate's answer and not a check this function makes.
func (s *InventoryService) settle(
	ctx context.Context,
	id string,
	change func(reservation *domain.Reservation) (bool, error),
	move func(ctx context.Context, sku domain.SKU, quantity domain.Quantity) error,
) (*domain.Reservation, error) {
	reservationID, err := domain.ParseReservationID(id)
	if err != nil {
		return nil, err
	}

	var settled *domain.Reservation

	err = s.tx.Do(ctx, func(ctx context.Context) error {
		reservation, err := s.reservations.FindByID(ctx, reservationID)
		if err != nil {
			return err
		}

		moved, err := change(reservation)
		if err != nil {
			return err
		}

		if !moved {
			settled = reservation

			return nil
		}

		for _, line := range reservation.Lines() {
			if err := move(ctx, line.SKU, line.Quantity); err != nil {
				return err
			}
		}

		settled, err = s.reservations.Update(ctx, reservation)

		return err
	})
	if err != nil {
		return nil, err
	}

	return settled, nil
}
