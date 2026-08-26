package grpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/common/v1"
	paymentv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/payment/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/errorx"
	"github.com/vasapolrittideah/system-design-ecommerce/pkg/grpcx"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/port/in"
)

// InitiatePayment starts one attempt to collect for the caller's own order.
func (h *PaymentHandler) InitiatePayment(
	ctx context.Context,
	req *paymentv1.InitiatePaymentRequest,
) (*paymentv1.InitiatePaymentResponse, error) {
	userID, err := caller(ctx)
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	started, err := h.payments.InitiatePayment(ctx, in.InitiatePaymentCommand{
		OrderID:        req.GetOrderId(),
		UserID:         userID,
		IdempotencyKey: req.GetIdempotencyKey(),
		Method:         req.GetMethod(),
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &paymentv1.InitiatePaymentResponse{
		Payment:       toProto(started.Payment),
		NextActionUrl: started.NextActionURL,
	}, nil
}

// GetPayment reads one of the caller's own attempts.
func (h *PaymentHandler) GetPayment(
	ctx context.Context,
	req *paymentv1.GetPaymentRequest,
) (*paymentv1.GetPaymentResponse, error) {
	userID, err := caller(ctx)
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	payment, err := h.payments.GetPayment(ctx, in.GetPaymentQuery{PaymentID: req.GetId(), UserID: userID})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	return &paymentv1.GetPaymentResponse{Payment: toProto(payment)}, nil
}

// GetPaymentsByOrderIDs reads the caller's own attempts against many orders.
func (h *PaymentHandler) GetPaymentsByOrderIDs(
	ctx context.Context,
	req *paymentv1.GetPaymentsByOrderIDsRequest,
) (*paymentv1.GetPaymentsByOrderIDsResponse, error) {
	userID, err := caller(ctx)
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	found, err := h.payments.GetPaymentsByOrderIDs(ctx, in.GetPaymentsByOrderIDsQuery{
		OrderIDs: req.GetOrderIds(),
		UserID:   userID,
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	payments := make([]*paymentv1.Payment, 0, len(found))
	for _, payment := range found {
		payments = append(payments, toProto(payment))
	}

	return &paymentv1.GetPaymentsByOrderIDsResponse{Payments: payments}, nil
}

// HandleProviderCallback settles the attempt a webhook names.
//
// The one method here that requires no caller, and that is not an oversight:
// the request originates at the payment provider and is forwarded by a BFF
// which has no identity to attach. What authenticates it is the signature over
// the body, checked inside — which is a stronger claim than a user id would
// have been, since it proves who wrote the bytes rather than who relayed them.
func (h *PaymentHandler) HandleProviderCallback(
	ctx context.Context,
	req *paymentv1.HandleProviderCallbackRequest,
) (*paymentv1.HandleProviderCallbackResponse, error) {
	payment, err := h.payments.HandleProviderCallback(ctx, in.ProviderCallbackCommand{
		Payload:   req.GetPayload(),
		Signature: req.GetSignature(),
	})
	if err != nil {
		return nil, errorx.ToGRPC(err)
	}

	// A callback naming an attempt this service does not have leaves this
	// empty rather than answering NotFound: providers send events for charges
	// made elsewhere, and a refusal makes them retry forever.
	return &paymentv1.HandleProviderCallbackResponse{Payment: toProto(payment)}, nil
}

// caller is who the request is for.
//
// It comes from the identity the interceptor put on the context — forwarded
// from the edge, where a BFF verified the token — and never from the request.
// Every method that uses it is about somebody's own payments, so a call
// arriving with no identity has nothing to be about: that is Unauthenticated
// rather than a listing of everybody's charges.
//
// This service does not verify tokens and does not answer 401 for an expired
// one. Identity was settled a hop earlier; what is missing here is a caller at
// all, which is what a relay looks like, and a relay has no business asking
// these questions.
func caller(ctx context.Context) (string, error) {
	identity, ok := grpcx.IdentityFrom(ctx)
	if !ok || identity.UserID == "" {
		return "", errorx.New(errorx.KindUnauthenticated, "this call is about the caller's own payments").
			WithReason("IDENTITY_REQUIRED")
	}

	return identity.UserID, nil
}

// toProto maps the aggregate onto the contract, and nil onto nil — which is
// what a callback about somebody else's charge answers with.
func toProto(payment *domain.Payment) *paymentv1.Payment {
	if payment == nil {
		return nil
	}

	return &paymentv1.Payment{
		Id:      payment.ID().String(),
		OrderId: payment.OrderID().String(),
		UserId:  payment.UserID().String(),
		Status:  toProtoStatus(payment.Status()),
		Amount: &commonv1.Money{
			AmountMinor:  payment.Amount().AmountMinor(),
			CurrencyCode: payment.Amount().Currency().String(),
		},
		ProviderReference: payment.ProviderReference().String(),
		FailureReason:     payment.FailureReason(),
		CreatedAt:         timestamppb.New(payment.CreatedAt()),
		UpdatedAt:         timestamppb.New(payment.UpdatedAt()),
	}
}

// toProtoStatus maps the domain's vocabulary onto the contract's.
//
// A state the contract does not name becomes UNSPECIFIED rather than a panic or
// a guess: an attempt in a state this build does not know about is still one a
// customer can be shown, and the alternative is a screen that fails on data a
// newer version wrote.
func toProtoStatus(status domain.Status) paymentv1.PaymentStatus {
	switch status {
	case domain.StatusPending:
		return paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING
	case domain.StatusSucceeded:
		return paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED
	case domain.StatusFailed:
		return paymentv1.PaymentStatus_PAYMENT_STATUS_FAILED
	default:
		return paymentv1.PaymentStatus_PAYMENT_STATUS_UNSPECIFIED
	}
}
