package rest

import (
	"time"

	paymentv1 "github.com/vasapolrittideah/system-design-ecommerce/gen/go/ecommerce/payment/v1"
)

// startPaymentRequest is what the pay button sends.
//
// No amount and no order id: the first is the order service's answer, and the
// second is in the path. What is left is the two things only the client knows —
// which key this attempt is being made under, and how the customer chose to pay.
type startPaymentRequest struct {
	// The client's own key for this attempt, echoed on every retry of it. One
	// per attempt the shopper makes: it deduplicates the submit and not the
	// order, so a customer whose card was declined and who tries again sends a
	// new key and gets a new attempt.
	IdempotencyKey string `json:"idempotencyKey" validate:"required,min=8,max=128"`

	// How the customer chose to pay, in this system's vocabulary rather than a
	// provider's. Omitted leaves it to the provider's default.
	//
	// Bounded and lowercased here and no further. Which names mean anything is
	// the provider adapter's answer and deliberately not a closed set — a shop
	// adds PromptPay without redeploying this tier — so the finer shape is
	// payment's to refuse, and a client meets it as the same 400 either way.
	Method string `json:"method" validate:"omitempty,max=32,lowercase"`
}

// providerCallbackHeaders is the webhook's signature, checked through the same
// `validate` tags a body goes through so the handler verifies nothing by hand.
type providerCallbackHeaders struct {
	Signature string `json:"x-provider-signature" validate:"required,max=1024"`
}

// What the payment screen answers with.
type (
	paymentResponse struct {
		Payment payment `json:"payment"`

		// Where to send the customer to finish paying — a 3-D Secure page, a
		// hosted form, a QR code. Absent when there is nothing for them to do.
		//
		// It is on the response and not on the payment because that is what it
		// is: a fact about this answer, which expires, and a URL authorising a
		// charge is not something to hand back on every later read.
		NextActionURL string `json:"nextActionUrl,omitempty"`
	}

	// payment is one attempt to collect, as the checkout screen needs it.
	//
	// The provider's reference and its failure reason are deliberately absent.
	// Both are written in the provider's vocabulary for whoever operates the
	// integration, and a storefront that rendered either would be showing a
	// shopper the machinery — and binding this API to a set of strings the
	// provider changes without telling anyone.
	payment struct {
		ID      string `json:"id"`
		OrderID string `json:"orderId"`
		Status  string `json:"status"`

		// What was sent to the provider, copied from the order when the attempt
		// started rather than read back now.
		Amount money `json:"amount"`

		CreatedAt time.Time `json:"createdAt"`
		UpdatedAt time.Time `json:"updatedAt"`
	}
)

// toPayment shapes one attempt for a screen.
func toPayment(p *paymentv1.Payment) payment {
	return payment{
		ID:        p.GetId(),
		OrderID:   p.GetOrderId(),
		Status:    toPaymentStatus(p.GetStatus()),
		Amount:    moneyOf(p.GetAmount()),
		CreatedAt: asTime(p.GetCreatedAt()),
		UpdatedAt: asTime(p.GetUpdatedAt()),
	}
}

// toPaymentStatus renders the enum as the lowercase name a client branches on,
// mapping a state this build does not know about to "unknown" for the reason
// toOrderStatus does.
func toPaymentStatus(status paymentv1.PaymentStatus) string {
	switch status {
	case paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING:
		return "pending"
	case paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED:
		return "succeeded"
	case paymentv1.PaymentStatus_PAYMENT_STATUS_FAILED:
		return "failed"
	default:
		return "unknown"
	}
}
