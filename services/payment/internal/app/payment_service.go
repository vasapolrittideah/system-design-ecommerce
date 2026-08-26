// Package app holds the use cases: the orchestration between a request and the
// domain rules that answer it. There is deliberately very little here — an `if`
// in this package that encodes a business policy is a rule that escaped the
// domain, where it could have been tested without a mock.
//
// This service orchestrates nothing beyond one charge. Order drives the
// checkout saga and decides what a failed payment means for an order; what
// happens here is that money is asked for, the answer is recorded, and the fact
// is published.
package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"

	"go.uber.org/zap"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/logger"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/port/out"
)

// maxOrderIDs bounds a batch read, matching the bound the proto declares. It is
// repeated here as a ceiling for callers that are not gRPC, since the cost of
// an unbounded read is paid by this process.
const maxOrderIDs = 100

// errReplay is how the transaction says "this key already has an answer".
//
// A sentinel rather than a value returned from the closure, because the
// transaction must roll back: the attempt it was about to write is not the one
// the client is going to be given.
var errReplay = errors.New("payment: idempotency key already answered")

// PaymentService implements the payment use cases.
type PaymentService struct {
	payments out.PaymentRepository
	idem     out.IdempotencyStore
	orders   out.OrderGateway
	provider out.ProviderGateway
	tx       out.TxManager
}

// Compile-time proof that the driving port is satisfied. Without it the failure
// surfaces in bootstrap, naming the wiring rather than the missing method.
var _ in.PaymentUseCase = (*PaymentService)(nil)

// NewPaymentService wires the use cases to their driven ports.
func NewPaymentService(
	payments out.PaymentRepository,
	idem out.IdempotencyStore,
	orders out.OrderGateway,
	provider out.ProviderGateway,
	tx out.TxManager,
) *PaymentService {
	return &PaymentService{payments: payments, idem: idem, orders: orders, provider: provider, tx: tx}
}

// InitiatePayment starts one attempt and returns as soon as the provider has
// accepted the request.
//
// The order of the steps is the whole design, and each is where it is because
// the next one would get it wrong:
//
//  1. A key already answered replays that answer before anything else happens.
//     This is what keeps a retry from asking a provider for money twice.
//  2. The order is read synchronously, because how much to charge is not a
//     number a client may send and an order already paid or cancelled must not
//     be charged for at all.
//  3. The claim and the attempt are written in one transaction, before the
//     provider is called. A call that times out with an unknown outcome has
//     then still left a record of itself, which is the only thing that makes
//     the charge reconcilable.
//  4. The provider is called outside any transaction. A network call with a
//     transaction open holds a connection and a row lock for as long as
//     somebody else's service takes to answer.
//  5. What the provider said is applied in a second transaction, on a freshly
//     locked row — the callback for this very charge can arrive before the
//     answer to the call that caused it.
func (s *PaymentService) InitiatePayment(
	ctx context.Context,
	cmd in.InitiatePaymentCommand,
) (in.InitiatedPayment, error) {
	userID, err := domain.ParseUserID(cmd.UserID)
	if err != nil {
		return in.InitiatedPayment{}, err
	}

	orderID, err := domain.ParseOrderID(cmd.OrderID)
	if err != nil {
		return in.InitiatedPayment{}, err
	}

	method, err := domain.NewMethod(cmd.Method)
	if err != nil {
		return in.InitiatedPayment{}, err
	}

	if cmd.IdempotencyKey == "" {
		return in.InitiatedPayment{}, errorx.New(errorx.KindInvalidInput, "idempotency_key is required").
			WithReason("IDEMPOTENCY_KEY_REQUIRED")
	}

	hash := hashRequest(userID, orderID, method)

	replayed, err := s.replay(ctx, userID, cmd.IdempotencyKey, hash)
	if err != nil || replayed != nil {
		return s.withNextAction(ctx, replayed), err
	}

	order, err := s.orders.GetOrder(ctx, orderID)
	if err != nil {
		return in.InitiatedPayment{}, err
	}
	if !order.Payable {
		return in.InitiatedPayment{}, errorx.New(errorx.KindConflict, "order is not waiting for payment").
			WithReason("ORDER_NOT_PAYABLE").
			WithMetadata(map[string]string{"order_id": orderID.String()})
	}

	// Minted before the row exists because the provider is given it as an
	// idempotency key: a retry after an ambiguous timeout can only reach the
	// same charge if the key predates the first call.
	paymentID := domain.NewPaymentID()

	built, err := domain.NewPayment(paymentID, orderID, userID, order.Total, method)
	if err != nil {
		return in.InitiatedPayment{}, err
	}

	var replayedID domain.PaymentID

	err = s.tx.Do(ctx, func(ctx context.Context) error {
		claim, taken, err := s.idem.Claim(ctx, userID, cmd.IdempotencyKey, hash, paymentID)
		if err != nil {
			return err
		}
		if !taken {
			if !bytes.Equal(claim.RequestHash, hash) {
				return errKeyReused()
			}

			replayedID = claim.PaymentID

			return errReplay
		}

		// What Create returns is deliberately dropped: the provider is called
		// next, and its answer is applied to a freshly locked row rather than
		// to this copy, which a callback may already have overtaken.
		_, err = s.payments.Create(ctx, built)

		return err
	})
	if err != nil {
		if errors.Is(err, errReplay) {
			found, err := s.payments.FindByID(ctx, replayedID)
			if err != nil {
				return in.InitiatedPayment{}, err
			}

			return s.withNextAction(ctx, found), nil
		}

		return in.InitiatedPayment{}, err
	}

	result, err := s.provider.Charge(ctx, out.ChargeCommand{
		PaymentID: paymentID,
		OrderID:   orderID,
		Amount:    order.Total,
		Method:    method,
	})
	if err != nil {
		// The attempt stays pending with no reference, which is exactly what it
		// is: the provider may have taken the money and may not have. Nothing
		// is guessed here — the record exists to be reconciled against, and the
		// caller is told the request did not complete.
		logger.From(ctx).Warn("payment: the provider did not answer, leaving the attempt pending",
			zap.String("payment_id", paymentID.String()),
			zap.Error(err),
		)

		return in.InitiatedPayment{}, err
	}

	settled, err := s.settle(ctx, paymentID, result)
	if err != nil {
		return in.InitiatedPayment{}, err
	}

	return in.InitiatedPayment{Payment: settled, NextActionURL: result.NextActionURL}, nil
}

// GetPayment reads one of the caller's own attempts.
func (s *PaymentService) GetPayment(ctx context.Context, query in.GetPaymentQuery) (*domain.Payment, error) {
	userID, err := domain.ParseUserID(query.UserID)
	if err != nil {
		return nil, err
	}

	paymentID, err := domain.ParsePaymentID(query.PaymentID)
	if err != nil {
		return nil, err
	}

	payment, err := s.payments.FindByID(ctx, paymentID)
	if err != nil {
		return nil, err
	}

	// Somebody else's attempt is not found rather than forbidden. Saying it
	// exists is saying something about another customer.
	if payment.UserID() != userID {
		return nil, domain.ErrPaymentNotFound
	}

	return payment, nil
}

// GetPaymentsByOrderIDs reads the caller's own attempts against many orders.
//
// Attempts belonging to anybody else are left out rather than refused: this
// fills a screen showing several orders at once, and one row that is not the
// caller's must not fail the whole thing.
func (s *PaymentService) GetPaymentsByOrderIDs(
	ctx context.Context,
	query in.GetPaymentsByOrderIDsQuery,
) ([]*domain.Payment, error) {
	userID, err := domain.ParseUserID(query.UserID)
	if err != nil {
		return nil, err
	}

	if len(query.OrderIDs) == 0 {
		return nil, errorx.New(errorx.KindInvalidInput, "order_ids is required")
	}
	if len(query.OrderIDs) > maxOrderIDs {
		return nil, errorx.New(errorx.KindInvalidInput, "order_ids has more than %d entries", maxOrderIDs)
	}

	orderIDs := make([]domain.OrderID, 0, len(query.OrderIDs))
	for _, raw := range query.OrderIDs {
		orderID, err := domain.ParseOrderID(raw)
		if err != nil {
			return nil, err
		}

		orderIDs = append(orderIDs, orderID)
	}

	found, err := s.payments.FindByOrderIDs(ctx, orderIDs)
	if err != nil {
		return nil, err
	}

	mine := make([]*domain.Payment, 0, len(found))
	for _, payment := range found {
		if payment.UserID() == userID {
			mine = append(mine, payment)
		}
	}

	return mine, nil
}

// HandleProviderCallback settles the attempt a webhook names.
//
// The signature is checked by the gateway before anything here sees the body,
// which is why this method takes bytes and not a parsed event: a body that has
// not been verified is not a fact about anything.
func (s *PaymentService) HandleProviderCallback(
	ctx context.Context,
	cmd in.ProviderCallbackCommand,
) (*domain.Payment, error) {
	callback, err := s.provider.ParseCallback(cmd.Payload, cmd.Signature)
	if err != nil {
		return nil, err
	}

	settled, err := s.settle(ctx, callback.PaymentID, out.ChargeResult{
		Reference:     callback.Reference,
		Status:        callback.Status,
		FailureReason: callback.FailureReason,
	})
	if err != nil {
		if errors.Is(err, domain.ErrPaymentNotFound) {
			// A charge made somewhere else, or one this service never asked
			// for. Providers send these, and refusing them makes the provider
			// retry forever.
			logger.From(ctx).Info("payment: callback names an attempt this service does not have",
				zap.String("payment_id", callback.PaymentID.String()),
			)

			return nil, nil
		}

		return nil, err
	}

	return settled, nil
}

// settle applies what the provider said to the attempt it said it about.
//
// The row is locked for the length of the transaction rather than read and then
// written on a version number, because the two writers that meet here are
// ordinary: the callback for a charge can arrive before the answer to the call
// that made it. Postgres does the waiting, and whichever arrives second reads
// the first one's outcome and finds there is nothing left to do — which the
// aggregate reports as "did not move" rather than as an error.
func (s *PaymentService) settle(
	ctx context.Context,
	paymentID domain.PaymentID,
	result out.ChargeResult,
) (*domain.Payment, error) {
	var settled *domain.Payment

	err := s.tx.Do(ctx, func(ctx context.Context) error {
		payment, err := s.payments.FindByIDForUpdate(ctx, paymentID)
		if err != nil {
			return err
		}

		moved, err := apply(payment, result)
		if err != nil {
			return err
		}

		settled = payment
		if !moved {
			return nil
		}

		settled, err = s.payments.Update(ctx, payment)

		return err
	})
	if err != nil {
		return nil, err
	}

	return settled, nil
}

// apply moves the aggregate the way the provider's answer says to, and reports
// whether anything changed.
//
// It is a switch over three outcomes and nothing more: which of them a provider
// meant is the gateway's translation, and what each one is allowed to do to an
// attempt is the aggregate's rule.
func apply(payment *domain.Payment, result out.ChargeResult) (bool, error) {
	switch result.Status {
	case domain.StatusSucceeded:
		return payment.Succeed(result.Reference)
	case domain.StatusFailed:
		moved, err := payment.Fail(result.FailureReason)
		if err != nil || !moved {
			return moved, err
		}

		// A declined charge still has a reference, and it is what somebody
		// reconciling a dispute looks the attempt up by.
		if result.Reference != "" {
			if _, err := payment.AttachProviderReference(result.Reference); err != nil {
				return false, err
			}
		}

		return true, nil
	default:
		// Still pending. There is nothing to settle, but the provider has now
		// named the charge and that name is what a reconciliation needs.
		return payment.AttachProviderReference(result.Reference)
	}
}

// replay answers a key that already has an answer, and reports nil when the key
// is free.
//
// It is a read outside any transaction, which makes it a hint rather than a
// decision: a key free here can be taken before the transaction below runs, and
// that transaction is where the question is settled. What it buys is the common
// case — a client retrying after a successful call is answered without asking
// anybody for money.
func (s *PaymentService) replay(
	ctx context.Context,
	userID domain.UserID,
	key string,
	hash []byte,
) (*domain.Payment, error) {
	claim, found, err := s.idem.Find(ctx, userID, key)
	if err != nil || !found {
		return nil, err
	}

	if !bytes.Equal(claim.RequestHash, hash) {
		return nil, errKeyReused()
	}

	return s.payments.FindByID(ctx, claim.PaymentID)
}

// withNextAction asks the provider where a replayed attempt's customer should
// go now.
//
// The URL is not stored, because it expires and because a link that authorises
// a charge is not something to return on every later read. A client replaying
// its key still needs one, so it is fetched at the time — and only for an
// attempt that is still waiting and that the provider has already named.
//
// A provider that will not answer costs the client the URL and not the request:
// the attempt itself is what was asked for, and it is returned either way.
func (s *PaymentService) withNextAction(ctx context.Context, payment *domain.Payment) in.InitiatedPayment {
	if payment == nil {
		return in.InitiatedPayment{}
	}

	if payment.Status() != domain.StatusPending || payment.ProviderReference() == "" {
		return in.InitiatedPayment{Payment: payment}
	}

	result, err := s.provider.Retrieve(ctx, payment.ProviderReference())
	if err != nil {
		logger.From(ctx).Warn("payment: could not refresh the next action for a replayed attempt",
			zap.String("payment_id", payment.ID().String()),
			zap.Error(err),
		)

		return in.InitiatedPayment{Payment: payment}
	}

	return in.InitiatedPayment{Payment: payment, NextActionURL: result.NextActionURL}
}

// hashRequest fingerprints what the client asked for, so that a key reused for
// a different order is told apart from a retry of the same one.
//
// The amount is deliberately not in it. It comes from the order service and can
// move between two attempts only if the order changed, which is a thing the
// order's own state machine has already refused — and a retry that hashed the
// amount would be answered as a different request for no reason a client could
// see.
func hashRequest(userID domain.UserID, orderID domain.OrderID, method domain.Method) []byte {
	digest := sha256.New()
	for _, part := range []string{userID.String(), orderID.String(), method.String()} {
		digest.Write([]byte{0})
		digest.Write([]byte(part))
	}

	return digest.Sum(nil)
}

// errKeyReused is the answer to a key that was first used for something else.
func errKeyReused() error {
	return errorx.New(errorx.KindConflict, "idempotency_key was used for a different request").
		WithReason("IDEMPOTENCY_KEY_REUSED")
}
