package grpc_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	paymentv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/payment/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
	adapter "github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/adapter/in/grpc"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/port/in"
)

// fakeUseCase is a hand-rolled test double rather than a mockery mock: nothing
// in .mockery.yml generates one for a driving port, which only ever has one
// real caller — the adapter that drives it — and here that caller is this test.
type fakeUseCase struct {
	payment *domain.Payment
	err     error
}

func (f *fakeUseCase) InitiatePayment(context.Context, in.InitiatePaymentCommand) (in.InitiatedPayment, error) {
	return in.InitiatedPayment{Payment: f.payment}, f.err
}

func (f *fakeUseCase) GetPayment(context.Context, in.GetPaymentQuery) (*domain.Payment, error) {
	return f.payment, f.err
}

func (f *fakeUseCase) GetPaymentsByOrderIDs(
	context.Context,
	in.GetPaymentsByOrderIDsQuery,
) ([]*domain.Payment, error) {
	return nil, f.err
}

func (f *fakeUseCase) HandleProviderCallback(
	context.Context,
	in.ProviderCallbackCommand,
) (*domain.Payment, error) {
	return f.payment, f.err
}

// A callback naming an attempt this service does not have settles nothing, and
// the use case answers nil. Mapping that to an empty response rather than
// dereferencing it is the difference between a provider stopping and a provider
// retrying a panic forever.
func TestHandleProviderCallbackAnswersAnEmptyPaymentForAnUnknownAttempt(t *testing.T) {
	handler := adapter.NewPaymentHandler(&fakeUseCase{payment: nil})

	res, err := handler.HandleProviderCallback(context.Background(),
		&paymentv1.HandleProviderCallbackRequest{Payload: []byte("{}"), Signature: "sig"})
	if err != nil {
		t.Fatalf("HandleProviderCallback() error = %v, want nil", err)
	}
	if res.GetPayment() != nil {
		t.Errorf("payment = %+v, want it empty", res.GetPayment())
	}
}

// The callback is the one method that requires no caller: it originates at the
// provider and is forwarded by a BFF with no identity to attach. What
// authenticates it is the signature over the body.
func TestHandleProviderCallbackNeedsNoIdentity(t *testing.T) {
	handler := adapter.NewPaymentHandler(&fakeUseCase{})

	if _, err := handler.HandleProviderCallback(context.Background(),
		&paymentv1.HandleProviderCallbackRequest{Payload: []byte("{}"), Signature: "sig"}); err != nil {
		t.Fatalf("HandleProviderCallback() error = %v, want nil", err)
	}
}

// Every other method is about somebody's own payments, so a call arriving with
// no identity has nothing to be about.
func TestTheOtherMethodsRefuseACallWithNoIdentity(t *testing.T) {
	handler := adapter.NewPaymentHandler(&fakeUseCase{})
	ctx := context.Background()

	calls := map[string]func() error{
		"InitiatePayment": func() error {
			_, err := handler.InitiatePayment(ctx, &paymentv1.InitiatePaymentRequest{})

			return err
		},
		"GetPayment": func() error {
			_, err := handler.GetPayment(ctx, &paymentv1.GetPaymentRequest{})

			return err
		},
		"GetPaymentsByOrderIDs": func() error {
			_, err := handler.GetPaymentsByOrderIDs(ctx, &paymentv1.GetPaymentsByOrderIDsRequest{})

			return err
		},
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("%s code = %v, want %v", name, status.Code(err), codes.Unauthenticated)
			}
		})
	}
}

// The identity comes off the context the interceptor populated, never from a
// field the client set.
func TestAnIdentityOnTheContextIsAccepted(t *testing.T) {
	handler := adapter.NewPaymentHandler(&fakeUseCase{})

	ctx := grpcx.IdentityInto(context.Background(), grpcx.Identity{
		UserID: "6f1c0a4e-2b8d-4c1a-9f3e-5d7b8a2c4e60",
	})

	if _, err := handler.GetPayment(ctx, &paymentv1.GetPaymentRequest{}); err != nil {
		t.Fatalf("GetPayment() error = %v, want nil", err)
	}
}
