// Package inbox is the other half of pkg/outbox: where the outbox promises an
// event will be delivered, the inbox records that it has been handled, so a
// second delivery of it does nothing.
//
// Kafka delivery is at-least-once and no consumer can opt out of it — a process
// that handled a batch and died before committing its offset reads that batch
// again. A consumer therefore claims the event before doing the work, in the
// transaction that does the work:
//
//	err := tx.Do(ctx, func(ctx context.Context) error {
//		claimed, err := inbox.Claim(ctx, txmanager.From(ctx, pool), group, envelope.EventId)
//		if err != nil || !claimed {
//			return err
//		}
//		return orders.MarkPaid(ctx, orderID, paidAt)   // aggregate + outbox rows
//	})
//
// Both go through one txmanager.DBTX, which is the whole point: claim on a
// separate connection and a process that dies mid-transaction has recorded
// having done work that rolled back, which loses the event for good.
//
// The table lives in the service's own migrations; schema.sql here is the
// definition to copy. Nothing in this package prunes it — retention has to
// outlive the topics a particular service consumes, and that is a fact about
// the service rather than about the claim.
package inbox

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
)

// ErrIncompleteClaim reports a claim that names no consumer group or no event.
// It is a programming error rather than a runtime condition: the same call
// would fail on every retry.
var ErrIncompleteClaim = errors.New("inbox: incomplete claim")

// claim is the statement both outcomes come from. The conflict target is named
// rather than left bare so that a violation of some constraint added later
// surfaces as an error instead of being read as a duplicate and dropping an
// event.
const claim = `INSERT INTO processed_events (consumer_group, event_id)
               VALUES ($1, $2)
               ON CONFLICT (consumer_group, event_id) DO NOTHING`

// Claim records that consumerGroup is handling eventID, reporting whether this
// call is the one that took it.
//
// False is the ordinary duplicate: some earlier delivery has already been
// handled, and the consumer should commit the offset and move on rather than
// treat it as a failure.
//
// Callers pass the txmanager.DBTX they are doing the work with, so the claim
// commits or rolls back with it. Two transactions racing for one event resolve
// on the primary key: the second blocks until the first ends, and then either
// sees the committed claim and returns false, or takes the claim the rollback
// released.
func Claim(ctx context.Context, db txmanager.DBTX, consumerGroup, eventID string) (bool, error) {
	if err := validate(consumerGroup, eventID); err != nil {
		return false, err
	}

	tag, err := db.Exec(ctx, claim, consumerGroup, eventID)
	if err != nil {
		return false, fmt.Errorf("inbox: claim event %q for %q: %w", eventID, consumerGroup, err)
	}

	return tag.RowsAffected() == 1, nil
}

// validate refuses an empty field rather than storing it, because an empty
// event ID is not a bad row but a claim every later event collides with: the
// first one is handled and the rest of the topic is skipped as already done.
func validate(consumerGroup, eventID string) error {
	var missing []string
	if consumerGroup == "" {
		missing = append(missing, "consumer group")
	}
	if eventID == "" {
		missing = append(missing, "event ID")
	}

	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrIncompleteClaim, strings.Join(missing, ", "))
	}

	return nil
}
