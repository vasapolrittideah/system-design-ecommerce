package app

import (
	"context"
	"errors"

	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/order/internal/port/out"
)

// defaultSweepLimit bounds a sweep whose caller did not say. The timer always
// says; this is here so that a limit of zero cannot mean "every stale order in
// one transaction".
const defaultSweepLimit = 100

// SagaTimeoutService ends orders nobody paid for in time.
//
// Its own service rather than a method on CheckoutSagaService, over the two
// ports it needs and none of the ones that one does: it consumes nothing, so it
// claims nothing in the inbox, and it calls nobody, because cancelling raises
// OrderCancelled and the reservation is given back by the consumer that reads
// it. Wiring it with a nil inbox and a nil inventory gateway to reuse a struct
// would be a constructor whose arguments a reader has to know to ignore.
type SagaTimeoutService struct {
	orders out.OrderRepository
	tx     out.TxManager
	policy Policy
}

// Compile-time proof that the driving port is satisfied.
var _ in.SagaTimeout = (*SagaTimeoutService)(nil)

// NewSagaTimeoutService wires the sweep to its driven ports.
func NewSagaTimeoutService(orders out.OrderRepository, tx out.TxManager, policy Policy) *SagaTimeoutService {
	return &SagaTimeoutService{orders: orders, tx: tx, policy: policy}
}

// ExpireStaleOrders cancels orders whose payment window has run out.
//
// One transaction for the whole batch, because the row locks ClaimStaleOrders
// takes are what stop a second worker working on the same orders, and they last
// exactly as long as it. The cancellations and the events announcing them go in
// with it, so a batch that fails announces nothing rather than telling inventory
// to give back stock for orders still marked pending.
//
// An order the aggregate refuses for one of two named reasons is skipped rather
// than failing the batch, because both are races this sweep lost and should
// lose: a payment landed between the claim and the transition, or the deadline
// this pass selected on is a moment the aggregate does not agree has arrived.
// Failing the batch over either would strand the rest behind an outcome that is
// already correct.
//
// Any other refusal fails the batch. The one it can raise is a window that is
// not a duration, which is a misconfiguration — and skipping it would leave the
// sweep reporting zero forever with nothing to say why.
func (s *SagaTimeoutService) ExpireStaleOrders(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = defaultSweepLimit
	}

	// Read once, so every order in this pass is judged against the moment the
	// rows were selected on rather than against the clock drifting through the
	// loop.
	now := s.policy.Now()

	var expired int

	err := s.tx.Do(ctx, func(ctx context.Context) error {
		expired = 0

		claimed, err := s.orders.ClaimStaleOrders(ctx, now.Add(-s.policy.PaymentWindow), limit)
		if err != nil {
			return err
		}

		for _, order := range claimed {
			moved, err := order.Expire(now, s.policy.PaymentWindow)
			if err != nil {
				if errors.Is(err, domain.ErrOrderPaid) || errors.Is(err, domain.ErrOrderNotExpired) {
					continue
				}

				return err
			}
			if !moved {
				continue
			}

			if _, err := s.orders.Update(ctx, order); err != nil {
				return err
			}

			expired++
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return expired, nil
}
