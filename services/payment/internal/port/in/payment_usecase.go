// Package in declares the driving ports: what can be asked of this service,
// stated without reference to how the asking arrives. A gRPC handler maps its
// request into one of the commands below and calls the interface.
package in

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
)

// InitiatePaymentCommand is a request to start collecting for an order.
type InitiatePaymentCommand struct {
	OrderID string

	// UserID is who is paying, taken from the verified identity on the call and
	// never from a field the client set.
	UserID string

	// IdempotencyKey is the client's own key for this submission. The same key
	// and the same body replay the first answer; the same key and a different
	// body are refused.
	//
	// It deduplicates the submit rather than the order: a customer whose card
	// was declined and who tries again sends a new key and gets a new attempt.
	IdempotencyKey string

	// Method is empty for whatever the provider defaults to.
	Method string
}

// InitiatedPayment is the attempt and where the customer has to go next.
type InitiatedPayment struct {
	Payment *domain.Payment

	// NextActionURL is empty when there is nothing for the customer to do and
	// the provider's answer is simply on its way.
	//
	// Not part of the aggregate: it expires, and a URL that authorises a charge
	// is not something to hand back on every later read of the attempt. A
	// client replaying its key gets a fresh one, asked for at the time.
	NextActionURL string
}

// GetPaymentQuery reads one attempt.
type GetPaymentQuery struct {
	PaymentID string

	// UserID is who is asking. An attempt that belongs to somebody else is
	// answered as not found rather than as forbidden: telling a caller that a
	// payment exists but is not theirs is telling them about another customer.
	UserID string
}

// GetPaymentsByOrderIDsQuery reads the attempts made against many orders, for a
// screen that shows several at once.
type GetPaymentsByOrderIDsQuery struct {
	OrderIDs []string

	// UserID is who is asking. Attempts belonging to anybody else are left out
	// of the answer rather than refused, for the reason a screen assembling
	// several rows must not fail over one of them.
	UserID string
}

// ProviderCallbackCommand is a webhook forwarded exactly as the provider sent
// it.
type ProviderCallbackCommand struct {
	// Payload is the provider's request body, byte for byte. The signature is
	// computed over exactly these bytes, so anything that decoded and
	// re-encoded them on the way here has produced a body that no longer
	// verifies.
	Payload []byte

	// Signature is the provider's own header.
	Signature string
}

// PaymentUseCase is everything this service can be asked to do.
type PaymentUseCase interface {
	// InitiatePayment starts one attempt and returns as soon as the provider
	// has accepted the request. It does not wait for the money: the outcome
	// arrives later as a callback.
	InitiatePayment(ctx context.Context, cmd InitiatePaymentCommand) (InitiatedPayment, error)

	// GetPayment reads one of the caller's own attempts.
	GetPayment(ctx context.Context, query GetPaymentQuery) (*domain.Payment, error)

	// GetPaymentsByOrderIDs reads the caller's own attempts against many
	// orders.
	GetPaymentsByOrderIDs(ctx context.Context, query GetPaymentsByOrderIDsQuery) ([]*domain.Payment, error)

	// HandleProviderCallback settles the attempt a webhook names.
	//
	// It returns nil for a callback naming an attempt this service does not
	// know about, which is not an error: providers send events for charges made
	// elsewhere, and answering those with a refusal makes them retry forever.
	HandleProviderCallback(ctx context.Context, cmd ProviderCallbackCommand) (*domain.Payment, error)
}
