package out

import (
	"context"

	"github.com/vasapolrittideah/system-design-ecommerce/services/payment/internal/domain"
)

// Claim is what a client's idempotency key was first used for.
type Claim struct {
	// RequestHash is of the request the key was first seen with. A retry
	// carrying the same key and a different body is a client bug rather than a
	// retry, and this is what tells the two apart.
	RequestHash []byte

	// PaymentID is the attempt that submission produced.
	PaymentID domain.PaymentID
}

// IdempotencyStore deduplicates client submissions.
//
// It is a table in this service's own database rather than a cache elsewhere,
// and that is the whole point: claiming the key and writing the attempt have to
// commit together. A claim that survived a rolled-back attempt would answer the
// client's retry with a replay of a charge that was never made.
//
// It deduplicates the submit and not the order. A customer whose card was
// declined and who tries again sends a new key and gets a new attempt, which is
// the behaviour the key is meant to allow rather than prevent.
type IdempotencyStore interface {
	// Find returns what a key was already used for, or false when it is free.
	//
	// It is the fast path for a retry that arrives after the first attempt
	// succeeded: answering from here costs one read and, more to the point,
	// asks no provider to charge anything. It is not the decision — a key free
	// at this moment can be taken before the transaction below runs.
	Find(ctx context.Context, userID domain.UserID, key string) (Claim, bool, error)

	// Claim takes the key for paymentID, reporting whether this call is the one
	// that took it. When it is not, the returned Claim is what the key already
	// holds.
	//
	// Callers run it inside the transaction that writes the attempt, which is
	// what makes the two atomic — and what makes a concurrent submit wait on
	// the primary key rather than race it.
	Claim(
		ctx context.Context,
		userID domain.UserID,
		key string,
		requestHash []byte,
		paymentID domain.PaymentID,
	) (Claim, bool, error)
}
