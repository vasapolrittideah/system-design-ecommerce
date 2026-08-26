package out

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
)

// ChargeCommand is one request for money.
type ChargeCommand struct {
	// PaymentID is this system's identifier for the attempt, and it is also
	// what the provider is given as its idempotency key: a retry after an
	// ambiguous timeout has to reach the same charge rather than make a second
	// one.
	PaymentID domain.PaymentID

	// OrderID is passed through so a charge can be found from an order in the
	// provider's own dashboard, by whoever is reconciling at the time.
	OrderID domain.OrderID

	Amount domain.Money

	// Method is empty for whatever the provider defaults to.
	Method domain.Method
}

// ChargeResult is what the provider said about a charge, in this service's
// vocabulary rather than its own.
//
// The translation is the adapter's whole job: a provider names its states and
// its decline reasons, and a use case that read them would be one that has to
// change when the shop signs with somebody else.
type ChargeResult struct {
	// Reference is what the provider calls this charge. Always present: a
	// provider that accepted a request without naming what it accepted has left
	// nothing to reconcile against, and the adapter refuses such an answer
	// rather than passing it on.
	Reference domain.ProviderReference

	// Status is pending, succeeded, or failed. Pending is the ordinary answer
	// and not a failure — a card may need a 3-D Secure page and a QR code needs
	// somebody to scan it.
	Status domain.Status

	// FailureReason is the provider's own words, empty unless Status is failed.
	FailureReason string

	// NextActionURL is where to send the customer to finish paying, empty when
	// there is nothing for them to do.
	NextActionURL string
}

// Callback is a webhook the provider sent, once its signature has been checked
// and its body understood.
type Callback struct {
	// PaymentID is the attempt the provider is talking about, recovered from
	// whatever the provider echoes back of what it was given.
	//
	// A callback about something this service never asked for parses fine and
	// names an attempt nobody has: providers send events for charges made
	// elsewhere, and the use case answers those with silence rather than an
	// error a provider would retry forever.
	PaymentID domain.PaymentID

	Reference domain.ProviderReference

	// Status is what the attempt is now. A callback that reports it still
	// pending is legitimate and settles nothing.
	Status domain.Status

	FailureReason string
}

// ProviderGateway is the payment provider, whoever it is.
//
// Every method here is idempotent at the far end, because every one of them is
// retried: Charge deduplicates on the payment id it is given, and the two reads
// are reads.
type ProviderGateway interface {
	// Charge asks for the money and returns as soon as the provider has
	// accepted the request — not when the money arrives.
	Charge(ctx context.Context, cmd ChargeCommand) (ChargeResult, error)

	// Retrieve asks what the provider now says about a charge it already knows.
	//
	// It answers two questions this service cannot answer alone: what a client
	// replaying its idempotency key should be sent to next, since a
	// next-action URL expires and is therefore not stored, and what actually
	// happened to a charge whose original call timed out.
	Retrieve(ctx context.Context, reference domain.ProviderReference) (ChargeResult, error)

	// ParseCallback verifies a webhook's signature and returns what it says.
	//
	// Verifying and parsing are one method because they are one decision: a
	// body that has not been checked against its signature is not a fact about
	// anything, and a shape that let a caller hold the parsed form before the
	// check had run would be one somebody eventually acts on.
	//
	// The payload is the provider's bytes exactly as they arrived. Anything
	// that decoded and re-encoded them on the way here has produced a body that
	// no longer verifies.
	ParseCallback(payload []byte, signature string) (Callback, error)
}
