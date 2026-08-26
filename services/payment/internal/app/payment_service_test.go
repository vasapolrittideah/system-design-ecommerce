package app_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/app"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/port/in"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/port/out"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/port/out/mocks"
)

const (
	userID      = "6f1c0a4e-2b8d-4c1a-9f3e-5d7b8a2c4e60"
	otherUserID = "7a2d1b5f-3c9e-4d2b-8a4f-6e8c9b3d5f71"
	orderID     = "8b3e2c6a-4d0f-4e3c-9b5a-7f9d0c4e6a82"
	paymentID   = "9c4f3d7b-5e1a-4f4d-ac6b-8a0e1d5f7b93"
	key         = "pay-0001"
	reference   = domain.ProviderReference("pi_3Nk9Xy2eZvKYlo2C0abcdefg")
)

// The mocks assert their own expectations on cleanup, so a call that was set up
// and never made fails the test — which is how "the provider was never asked
// for money" below is checked without asserting on a counter.
func setup(t *testing.T) (
	*mocks.MockPaymentRepository,
	*mocks.MockIdempotencyStore,
	*mocks.MockOrderGateway,
	*mocks.MockProviderGateway,
	*mocks.MockTxManager,
	*app.PaymentService,
) {
	t.Helper()

	payments := mocks.NewMockPaymentRepository(t)
	idem := mocks.NewMockIdempotencyStore(t)
	orders := mocks.NewMockOrderGateway(t)
	provider := mocks.NewMockProviderGateway(t)
	tx := mocks.NewMockTxManager(t)

	return payments, idem, orders, provider, tx,
		app.NewPaymentService(payments, idem, orders, provider, tx)
}

func expectTx(tx *mocks.MockTxManager) {
	tx.EXPECT().
		Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		})
}

func thb(t *testing.T, amountMinor int64) domain.Money {
	t.Helper()

	currency, err := domain.NewCurrencyCode("THB")
	if err != nil {
		t.Fatalf("NewCurrencyCode() error = %v, want nil", err)
	}

	money, err := domain.NewMoney(amountMinor, currency)
	if err != nil {
		t.Fatalf("NewMoney() error = %v, want nil", err)
	}

	return money
}

func command() in.InitiatePaymentCommand {
	return in.InitiatePaymentCommand{
		OrderID:        orderID,
		UserID:         userID,
		IdempotencyKey: key,
		Method:         "card",
	}
}

func payableOrder(t *testing.T) out.PayableOrder {
	t.Helper()

	return out.PayableOrder{ID: domain.OrderID(orderID), Total: thb(t, 99800), Payable: true}
}

// pending builds the attempt as it is right after Create, before the provider
// has answered.
func pending(t *testing.T) *domain.Payment {
	t.Helper()

	built, err := domain.NewPayment(domain.PaymentID(paymentID), domain.OrderID(orderID),
		domain.UserID(userID), thb(t, 99800), "card")
	if err != nil {
		t.Fatalf("NewPayment() error = %v, want nil", err)
	}

	return built
}

func expectFreeKey(idem *mocks.MockIdempotencyStore) {
	idem.EXPECT().
		Find(mock.Anything, domain.UserID(userID), key).
		Return(out.Claim{}, false, nil)
}

func expectClaimTaken(idem *mocks.MockIdempotencyStore) {
	idem.EXPECT().
		Claim(mock.Anything, domain.UserID(userID), key, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			_ domain.UserID,
			_ string,
			hash []byte,
			id domain.PaymentID,
		) (out.Claim, bool, error) {
			return out.Claim{RequestHash: hash, PaymentID: id}, true, nil
		})
}

func TestInitiatePaymentChargesAndSettles(t *testing.T) {
	payments, idem, orders, provider, tx, service := setup(t)
	expectTx(tx)
	expectFreeKey(idem)
	expectClaimTaken(idem)

	orders.EXPECT().GetOrder(mock.Anything, domain.OrderID(orderID)).Return(payableOrder(t), nil)
	payments.EXPECT().Create(mock.Anything, mock.Anything).Return(pending(t), nil)

	// The provider settles synchronously, which a card charge with no 3-D
	// Secure step does.
	provider.EXPECT().
		Charge(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, cmd out.ChargeCommand) (out.ChargeResult, error) {
			// The payment id is the provider's idempotency key, so a retry
			// after an ambiguous timeout reaches the same charge.
			if cmd.PaymentID == "" {
				t.Error("Charge() was given no payment id to deduplicate on")
			}
			if cmd.Amount.AmountMinor() != 99800 {
				t.Errorf("Charge() amount = %d, want the order's 99800", cmd.Amount.AmountMinor())
			}

			return out.ChargeResult{Reference: reference, Status: domain.StatusSucceeded}, nil
		})

	loaded := pending(t)
	payments.EXPECT().FindByIDForUpdate(mock.Anything, mock.Anything).Return(loaded, nil)
	payments.EXPECT().
		Update(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, p *domain.Payment) (*domain.Payment, error) {
			return p, nil
		})

	got, err := service.InitiatePayment(context.Background(), command())
	if err != nil {
		t.Fatalf("InitiatePayment() error = %v, want nil", err)
	}
	if got.Payment.Status() != domain.StatusSucceeded {
		t.Errorf("status = %q, want %q", got.Payment.Status(), domain.StatusSucceeded)
	}
	if got.Payment.ProviderReference() != reference {
		t.Errorf("provider reference = %q, want %q", got.Payment.ProviderReference(), reference)
	}
}

// The ordinary answer: the provider accepted the request and the customer has
// somewhere to go. Nothing is settled, and the reference is recorded so the
// charge can be reconciled.
func TestInitiatePaymentLeavesAPendingChargePending(t *testing.T) {
	payments, idem, orders, provider, tx, service := setup(t)
	expectTx(tx)
	expectFreeKey(idem)
	expectClaimTaken(idem)

	orders.EXPECT().GetOrder(mock.Anything, mock.Anything).Return(payableOrder(t), nil)
	payments.EXPECT().Create(mock.Anything, mock.Anything).Return(pending(t), nil)
	provider.EXPECT().Charge(mock.Anything, mock.Anything).Return(out.ChargeResult{
		Reference:     reference,
		Status:        domain.StatusPending,
		NextActionURL: "https://provider.test/3ds/abc",
	}, nil)

	payments.EXPECT().FindByIDForUpdate(mock.Anything, mock.Anything).Return(pending(t), nil)
	payments.EXPECT().
		Update(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, p *domain.Payment) (*domain.Payment, error) {
			return p, nil
		})

	got, err := service.InitiatePayment(context.Background(), command())
	if err != nil {
		t.Fatalf("InitiatePayment() error = %v, want nil", err)
	}
	if got.Payment.Status() != domain.StatusPending {
		t.Errorf("status = %q, want %q", got.Payment.Status(), domain.StatusPending)
	}
	if got.NextActionURL != "https://provider.test/3ds/abc" {
		t.Errorf("next action = %q, want the provider's URL", got.NextActionURL)
	}
}

// An order the order service says is no longer payable stops the flow before
// anything is written and before anybody is asked for money.
func TestInitiatePaymentRefusesAnOrderThatIsNotPayable(t *testing.T) {
	_, idem, orders, _, _, service := setup(t)
	expectFreeKey(idem)

	orders.EXPECT().GetOrder(mock.Anything, mock.Anything).Return(out.PayableOrder{
		ID:      domain.OrderID(orderID),
		Total:   thb(t, 99800),
		Payable: false,
	}, nil)

	_, err := service.InitiatePayment(context.Background(), command())
	if errorx.KindOf(err) != errorx.KindConflict {
		t.Fatalf("InitiatePayment() error kind = %v, want %v", errorx.KindOf(err), errorx.KindConflict)
	}
	if errorx.Reason(err) != "ORDER_NOT_PAYABLE" {
		t.Errorf("reason = %q, want %q", errorx.Reason(err), "ORDER_NOT_PAYABLE")
	}
}

// A retry that arrives after the first attempt was written is answered from the
// record, without asking the provider for money a second time. The provider
// mock has no Charge expectation, so mockery's own cleanup check is what proves
// it was never called.
func TestInitiatePaymentReplaysAKeyThatWasAlreadyAnswered(t *testing.T) {
	payments, idem, _, _, _, service := setup(t)

	settled := pending(t)
	if _, err := settled.Succeed(reference); err != nil {
		t.Fatalf("Succeed() error = %v, want nil", err)
	}

	idem.EXPECT().
		Find(mock.Anything, domain.UserID(userID), key).
		RunAndReturn(func(_ context.Context, _ domain.UserID, _ string) (out.Claim, bool, error) {
			return out.Claim{RequestHash: requestHash(t, command()), PaymentID: domain.PaymentID(paymentID)}, true, nil
		})
	payments.EXPECT().FindByID(mock.Anything, domain.PaymentID(paymentID)).Return(settled, nil)

	got, err := service.InitiatePayment(context.Background(), command())
	if err != nil {
		t.Fatalf("InitiatePayment() error = %v, want nil", err)
	}
	if got.Payment.Status() != domain.StatusSucceeded {
		t.Errorf("status = %q, want the first answer replayed", got.Payment.Status())
	}
	// A settled attempt has nowhere left to send the customer, so the provider
	// is not asked for a next action either.
	if got.NextActionURL != "" {
		t.Errorf("next action = %q, want it empty for a settled attempt", got.NextActionURL)
	}

}

// A replayed attempt that is still waiting needs somewhere to send the
// customer, and the URL is not stored because it expires — so it is asked for
// at the time.
func TestInitiatePaymentRefreshesTheNextActionOnReplay(t *testing.T) {
	payments, idem, _, provider, _, service := setup(t)

	waiting := pending(t)
	if _, err := waiting.AttachProviderReference(reference); err != nil {
		t.Fatalf("AttachProviderReference() error = %v, want nil", err)
	}

	idem.EXPECT().
		Find(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ domain.UserID, _ string) (out.Claim, bool, error) {
			return out.Claim{RequestHash: requestHash(t, command()), PaymentID: domain.PaymentID(paymentID)}, true, nil
		})
	payments.EXPECT().FindByID(mock.Anything, mock.Anything).Return(waiting, nil)
	provider.EXPECT().Retrieve(mock.Anything, reference).Return(out.ChargeResult{
		Reference:     reference,
		Status:        domain.StatusPending,
		NextActionURL: "https://provider.test/3ds/fresh",
	}, nil)

	got, err := service.InitiatePayment(context.Background(), command())
	if err != nil {
		t.Fatalf("InitiatePayment() error = %v, want nil", err)
	}
	if got.NextActionURL != "https://provider.test/3ds/fresh" {
		t.Errorf("next action = %q, want the freshly fetched URL", got.NextActionURL)
	}
}

// A provider that will not answer costs the client the URL and not the request:
// the attempt is what was asked for, and it comes back either way.
func TestInitiatePaymentSurvivesAProviderThatCannotRefreshTheNextAction(t *testing.T) {
	payments, idem, _, provider, _, service := setup(t)

	waiting := pending(t)
	if _, err := waiting.AttachProviderReference(reference); err != nil {
		t.Fatalf("AttachProviderReference() error = %v, want nil", err)
	}

	idem.EXPECT().
		Find(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ domain.UserID, _ string) (out.Claim, bool, error) {
			return out.Claim{RequestHash: requestHash(t, command()), PaymentID: domain.PaymentID(paymentID)}, true, nil
		})
	payments.EXPECT().FindByID(mock.Anything, mock.Anything).Return(waiting, nil)
	provider.EXPECT().Retrieve(mock.Anything, mock.Anything).Return(out.ChargeResult{}, errors.New("provider down"))

	got, err := service.InitiatePayment(context.Background(), command())
	if err != nil {
		t.Fatalf("InitiatePayment() error = %v, want nil", err)
	}
	if got.Payment == nil {
		t.Fatal("payment = nil, want the attempt back regardless")
	}
	if got.NextActionURL != "" {
		t.Errorf("next action = %q, want it empty when the provider would not answer", got.NextActionURL)
	}
}

// The same key with a different body is a client bug rather than a retry.
func TestInitiatePaymentRefusesAKeyReusedForADifferentOrder(t *testing.T) {
	_, idem, _, _, _, service := setup(t)

	idem.EXPECT().
		Find(mock.Anything, mock.Anything, mock.Anything).
		Return(out.Claim{RequestHash: []byte("a different request"), PaymentID: domain.PaymentID(paymentID)}, true, nil)

	_, err := service.InitiatePayment(context.Background(), command())
	if errorx.Reason(err) != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("reason = %q, want %q", errorx.Reason(err), "IDEMPOTENCY_KEY_REUSED")
	}
}

// A provider that does not answer leaves the attempt written and pending. That
// is the point of writing it first: the charge may or may not have been made,
// and a record that exists is the only thing that makes it reconcilable.
func TestInitiatePaymentLeavesTheAttemptWhenTheProviderFails(t *testing.T) {
	payments, idem, orders, provider, tx, service := setup(t)
	expectTx(tx)
	expectFreeKey(idem)
	expectClaimTaken(idem)

	orders.EXPECT().GetOrder(mock.Anything, mock.Anything).Return(payableOrder(t), nil)

	created := false
	payments.EXPECT().
		Create(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, p *domain.Payment) (*domain.Payment, error) {
			created = true

			return p, nil
		})

	wantErr := errors.New("provider timed out")
	provider.EXPECT().Charge(mock.Anything, mock.Anything).Return(out.ChargeResult{}, wantErr)

	_, err := service.InitiatePayment(context.Background(), command())
	if !errors.Is(err, wantErr) {
		t.Fatalf("InitiatePayment() error = %v, want %v", err, wantErr)
	}
	if !created {
		t.Error("the attempt was not written before the provider was called")
	}
}

func TestInitiatePaymentRejectsWhatIsNotARequest(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*in.InitiatePaymentCommand)
		wantErr errorx.Kind
	}{
		{"no user", func(c *in.InitiatePaymentCommand) { c.UserID = "" }, errorx.KindInvalidInput},
		{"no order", func(c *in.InitiatePaymentCommand) { c.OrderID = "" }, errorx.KindInvalidInput},
		{"malformed order", func(c *in.InitiatePaymentCommand) { c.OrderID = "nope" }, errorx.KindInvalidInput},
		{"no key", func(c *in.InitiatePaymentCommand) { c.IdempotencyKey = "" }, errorx.KindInvalidInput},
		{"bad method", func(c *in.InitiatePaymentCommand) { c.Method = "2c2p" }, errorx.KindInvalidInput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, _, _, service := setup(t)

			cmd := command()
			tt.mutate(&cmd)

			_, err := service.InitiatePayment(context.Background(), cmd)
			if errorx.KindOf(err) != tt.wantErr {
				t.Fatalf("InitiatePayment() error kind = %v, want %v", errorx.KindOf(err), tt.wantErr)
			}
		})
	}
}

func TestHandleProviderCallbackSettlesTheAttempt(t *testing.T) {
	payments, _, _, provider, tx, service := setup(t)
	expectTx(tx)

	provider.EXPECT().
		ParseCallback([]byte("{}"), "sig").
		Return(out.Callback{
			PaymentID: domain.PaymentID(paymentID),
			Reference: reference,
			Status:    domain.StatusSucceeded,
		}, nil)
	payments.EXPECT().FindByIDForUpdate(mock.Anything, domain.PaymentID(paymentID)).Return(pending(t), nil)
	payments.EXPECT().
		Update(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, p *domain.Payment) (*domain.Payment, error) {
			return p, nil
		})

	got, err := service.HandleProviderCallback(context.Background(), in.ProviderCallbackCommand{
		Payload:   []byte("{}"),
		Signature: "sig",
	})
	if err != nil {
		t.Fatalf("HandleProviderCallback() error = %v, want nil", err)
	}
	if got.Status() != domain.StatusSucceeded {
		t.Errorf("status = %q, want %q", got.Status(), domain.StatusSucceeded)
	}
}

// A redelivery finds the attempt settled, changes nothing, and writes nothing.
// The repository has no Update expectation, so mockery proves the row was left
// alone.
func TestHandleProviderCallbackIsIdempotent(t *testing.T) {
	payments, _, _, provider, tx, service := setup(t)
	expectTx(tx)

	settled := pending(t)
	if _, err := settled.Succeed(reference); err != nil {
		t.Fatalf("Succeed() error = %v, want nil", err)
	}
	settled.PullEvents()

	provider.EXPECT().ParseCallback(mock.Anything, mock.Anything).Return(out.Callback{
		PaymentID: domain.PaymentID(paymentID),
		Reference: reference,
		Status:    domain.StatusSucceeded,
	}, nil)
	payments.EXPECT().FindByIDForUpdate(mock.Anything, mock.Anything).Return(settled, nil)

	got, err := service.HandleProviderCallback(context.Background(), in.ProviderCallbackCommand{
		Payload:   []byte("{}"),
		Signature: "sig",
	})
	if err != nil {
		t.Fatalf("HandleProviderCallback() error = %v, want nil", err)
	}
	if got.Status() != domain.StatusSucceeded {
		t.Errorf("status = %q, want it left %q", got.Status(), domain.StatusSucceeded)
	}
}

// Providers send events for charges made elsewhere. Refusing them makes the
// provider retry forever, so an attempt this service does not have is answered
// with silence.
func TestHandleProviderCallbackIgnoresAnAttemptItDoesNotHave(t *testing.T) {
	payments, _, _, provider, tx, service := setup(t)
	expectTx(tx)

	provider.EXPECT().ParseCallback(mock.Anything, mock.Anything).Return(out.Callback{
		PaymentID: domain.PaymentID(paymentID),
		Reference: "chrg_somebody_elses",
		Status:    domain.StatusSucceeded,
	}, nil)
	payments.EXPECT().FindByIDForUpdate(mock.Anything, mock.Anything).Return(nil, domain.ErrPaymentNotFound)

	got, err := service.HandleProviderCallback(context.Background(), in.ProviderCallbackCommand{
		Payload:   []byte("{}"),
		Signature: "sig",
	})
	if err != nil {
		t.Fatalf("HandleProviderCallback() error = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("payment = %+v, want nil", got)
	}
}

// A body that does not verify never reaches the aggregate. The repository has
// no expectations at all, which is what proves it.
func TestHandleProviderCallbackRefusesABodyThatDoesNotVerify(t *testing.T) {
	_, _, _, provider, _, service := setup(t)

	wantErr := errorx.New(errorx.KindUnauthenticated, "signature does not verify")
	provider.EXPECT().ParseCallback(mock.Anything, mock.Anything).Return(out.Callback{}, wantErr)

	_, err := service.HandleProviderCallback(context.Background(), in.ProviderCallbackCommand{
		Payload:   []byte("tampered"),
		Signature: "wrong",
	})
	if errorx.KindOf(err) != errorx.KindUnauthenticated {
		t.Fatalf("error kind = %v, want %v", errorx.KindOf(err), errorx.KindUnauthenticated)
	}
}

func TestGetPaymentAnswersSomebodyElsesAsNotFound(t *testing.T) {
	payments, _, _, _, _, service := setup(t)

	payments.EXPECT().FindByID(mock.Anything, domain.PaymentID(paymentID)).Return(pending(t), nil)

	_, err := service.GetPayment(context.Background(), in.GetPaymentQuery{
		PaymentID: paymentID,
		UserID:    otherUserID,
	})
	if !errors.Is(err, domain.ErrPaymentNotFound) {
		t.Fatalf("GetPayment() error = %v, want %v", err, domain.ErrPaymentNotFound)
	}
}

// One row that is not the caller's must not fail a screen assembling several.
func TestGetPaymentsByOrderIDsLeavesOutWhatIsNotTheCallers(t *testing.T) {
	payments, _, _, _, _, service := setup(t)

	mine := pending(t)

	theirs, err := domain.NewPayment(domain.NewPaymentID(), domain.OrderID(orderID),
		domain.UserID(otherUserID), thb(t, 100), "card")
	if err != nil {
		t.Fatalf("NewPayment() error = %v, want nil", err)
	}

	payments.EXPECT().
		FindByOrderIDs(mock.Anything, []domain.OrderID{domain.OrderID(orderID)}).
		Return([]*domain.Payment{mine, theirs}, nil)

	got, err := service.GetPaymentsByOrderIDs(context.Background(), in.GetPaymentsByOrderIDsQuery{
		OrderIDs: []string{orderID},
		UserID:   userID,
	})
	if err != nil {
		t.Fatalf("GetPaymentsByOrderIDs() error = %v, want nil", err)
	}
	if len(got) != 1 || got[0].ID() != mine.ID() {
		t.Fatalf("got %d payments, want only the caller's own", len(got))
	}
}

func TestGetPaymentsByOrderIDsRejectsAnEmptyBatch(t *testing.T) {
	_, _, _, _, _, service := setup(t)

	_, err := service.GetPaymentsByOrderIDs(context.Background(), in.GetPaymentsByOrderIDsQuery{
		OrderIDs: nil,
		UserID:   userID,
	})
	if errorx.KindOf(err) != errorx.KindInvalidInput {
		t.Fatalf("error kind = %v, want %v", errorx.KindOf(err), errorx.KindInvalidInput)
	}
}

// requestHash reproduces the fingerprint the service computes for a command.
//
// Computed here rather than observed, so the two derivations are independent: a
// change to the production one that this does not follow shows up as a replay
// being refused as a reused key, which is the failure that would matter.
func requestHash(t *testing.T, cmd in.InitiatePaymentCommand) []byte {
	t.Helper()

	digest := sha256.New()
	for _, part := range []string{cmd.UserID, cmd.OrderID, cmd.Method} {
		digest.Write([]byte{0})
		digest.Write([]byte(part))
	}

	return digest.Sum(nil)
}
