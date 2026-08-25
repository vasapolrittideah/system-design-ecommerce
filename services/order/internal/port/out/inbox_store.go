package out

import "context"

// InboxStore records that this service has handled one delivery of an event,
// so a redelivery — which at-least-once delivery guarantees will eventually
// send — does no work a second time.
type InboxStore interface {
	// Claim takes eventID for consumerGroup, reporting whether this call is
	// the one that took it. Run inside the transaction that does the work the
	// event triggers, so a claim never outlives an effect that rolled back.
	Claim(ctx context.Context, consumerGroup, eventID string) (bool, error)
}
