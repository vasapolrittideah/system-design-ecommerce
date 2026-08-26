// Package grpc is the driving adapter: it maps ecommerce.payment.v1 messages
// onto use case commands and back, and nothing else.
//
// Three absences are deliberate. It validates nothing, because the constraints
// are declared in the proto and enforced by an interceptor; it constructs no
// business errors, because every failure arrives already classified and leaves
// through errorx.ToGRPC; and it takes the caller's identity from the context
// rather than from a field, because a user id a client could set is a user id a
// client could change.
package grpc

import (
	paymentv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/payment/v1"
	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/port/in"
)

// PaymentHandler serves PaymentService.
//
// Embedding the generated Unimplemented struct lets a new RPC be added to the
// proto without breaking the build here: the method answers Unimplemented until
// someone writes it.
type PaymentHandler struct {
	paymentv1.UnimplementedPaymentServiceServer

	payments in.PaymentUseCase
}

var _ paymentv1.PaymentServiceServer = (*PaymentHandler)(nil)

// NewPaymentHandler builds the handler over the driving port.
func NewPaymentHandler(payments in.PaymentUseCase) *PaymentHandler {
	return &PaymentHandler{payments: payments}
}
