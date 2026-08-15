// Package outbox stores the events a service is about to publish in the same
// database, and in the same transaction, as the change that produced them.
//
// Committing an aggregate and publishing to Kafka are two systems and cannot be
// made atomic: whichever order they are attempted in, a process that dies in
// between leaves either an order nobody was told about or a payment for an order
// that was rolled back. Writing the event as a row takes the second system off
// the critical path, and a relay drains the table afterwards.
//
// A repository writes the events its aggregate accumulated:
//
//	func (r *OrderRepo) Save(ctx context.Context, o *domain.Order) error {
//		db := txmanager.From(ctx, r.pool)
//		// ... persist the aggregate through db ...
//		return outbox.Write(ctx, db, records...)
//	}
//
// Both writes go through the same txmanager.DBTX, which is the whole point: run
// them on separate connections and the guarantee is gone.
//
// The table lives in the service's own migrations; schema.sql here is the
// definition to copy. This package never unmarshals a payload — the day it needs
// to, business logic has leaked into infrastructure.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/vasapolrittideah/system-design-ecommerce/pkg/txmanager"
)

// ErrIncompleteRecord reports a record that cannot be published for want of a
// field. It is a programming error rather than a runtime condition: the same
// record would fail on every retry.
var ErrIncompleteRecord = errors.New("outbox: incomplete record")

// Record is one event waiting to be published.
//
// There is no message key: the key is always the aggregate ID, because
// per-aggregate ordering is the one ordering guarantee the system relies on and
// it disappears the moment two events for one order are keyed differently.
type Record struct {
	// AggregateType and AggregateID identify what the event happened to, e.g.
	// "order" and the order's UUID.
	AggregateType string
	AggregateID   string

	// EventType is the name consumers dispatch on, e.g. "OrderPaid". Events are
	// facts that already happened, never instructions to do something.
	EventType string

	// Topic is where the relay publishes the row. The service that owns the
	// event decides it, so adding an event never means editing shared code.
	Topic string

	// Payload is the marshalled protobuf. Nothing in this package reads it.
	Payload []byte

	// Headers travels with the message, carrying traceparent and
	// correlation_id so a trace survives the hop through Kafka. Nil is fine.
	Headers map[string]string
}

func (r Record) validate() error {
	var missing []string
	for _, f := range []struct {
		name  string
		empty bool
	}{
		{"AggregateType", r.AggregateType == ""},
		{"AggregateID", r.AggregateID == ""},
		{"EventType", r.EventType == ""},
		{"Topic", r.Topic == ""},
		{"Payload", len(r.Payload) == 0},
	} {
		if f.empty {
			missing = append(missing, f.name)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrIncompleteRecord, strings.Join(missing, ", "))
	}

	return nil
}

// columns is how many placeholders each record contributes to the statement.
const columns = 6

// Write appends records to the outbox through db.
//
// Callers pass the txmanager.DBTX they are already writing the aggregate with,
// so the events land in that transaction. Writing no records is not an error.
//
// All records go in one statement rather than a round trip each, because the
// transaction holds its locks for the whole exchange.
func Write(ctx context.Context, db txmanager.DBTX, records ...Record) error {
	if len(records) == 0 {
		return nil
	}

	var stmt strings.Builder
	stmt.WriteString(`INSERT INTO outbox (aggregate_type, aggregate_id, event_type, topic, payload, headers) VALUES `)

	args := make([]any, 0, len(records)*columns)

	for i, record := range records {
		if err := record.validate(); err != nil {
			return fmt.Errorf("outbox: record %d: %w", i, err)
		}

		headers, err := marshalHeaders(record.Headers)
		if err != nil {
			return fmt.Errorf("outbox: record %d: %w", i, err)
		}

		if i > 0 {
			stmt.WriteString(", ")
		}
		stmt.WriteByte('(')
		for c := range columns {
			if c > 0 {
				stmt.WriteByte(',')
			}
			stmt.WriteByte('$')
			stmt.WriteString(strconv.Itoa(i*columns + c + 1))
		}
		stmt.WriteByte(')')

		args = append(args,
			record.AggregateType,
			record.AggregateID,
			record.EventType,
			record.Topic,
			record.Payload,
			headers,
		)
	}

	if _, err := db.Exec(ctx, stmt.String(), args...); err != nil {
		return fmt.Errorf("outbox: write %d record(s): %w", len(records), err)
	}

	return nil
}

// marshalHeaders keeps an absent header set as an empty object rather than the
// JSON null encoding/json produces for a nil map, so consumers can read the
// column without checking for null first.
func marshalHeaders(headers map[string]string) ([]byte, error) {
	if len(headers) == 0 {
		return []byte("{}"), nil
	}

	encoded, err := json.Marshal(headers)
	if err != nil {
		return nil, fmt.Errorf("marshal headers: %w", err)
	}

	return encoded, nil
}
