-- The outbox is how a service announces that something happened: the aggregate
-- and the events describing it are written in the same transaction, and a relay
-- publishes the rows to Kafka afterwards. Committing the aggregate is therefore
-- the same act as promising the event will be delivered.
--
-- Every service owns its own database, so this table is copied into each
-- service's own migrations under services/<name>/db/migrations/ rather than
-- living in one shared schema.

CREATE TABLE outbox (
    -- An identity column, not the uuid every other table carries: the relay
    -- publishes in insertion order and a uuid supplies none. There is no
    -- version column either, because a row is written once and only ever
    -- updated to record that it was published.
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    -- Which aggregate the event came from. aggregate_id doubles as the Kafka
    -- message key, which is what keeps events for one order in order.
    aggregate_type text        NOT NULL,
    aggregate_id   text        NOT NULL,

    -- The event name as consumers know it, e.g. OrderPaid.
    event_type     text        NOT NULL,

    -- The destination topic is stored per row rather than derived by the relay,
    -- so deciding where an event belongs stays with the service that owns the
    -- event instead of turning into a lookup table shared by everyone.
    topic          text        NOT NULL,

    -- Marshalled protobuf. Nothing between here and the consumer looks inside
    -- it, which is what lets the payload evolve without touching the plumbing.
    payload        bytea       NOT NULL,

    -- Carries traceparent and correlation_id across the async hop, where the
    -- context that produced the event is long gone.
    headers        jsonb       NOT NULL DEFAULT '{}'::jsonb,

    created_at     timestamptz NOT NULL DEFAULT now(),

    -- NULL until the relay has published the row. It is the queue's only state.
    published_at   timestamptz
);

-- The relay asks for unpublished rows and nothing else, and once the service
-- has been running for a day those are a vanishing fraction of the table. A
-- partial index keeps that query on an index whose size tracks the backlog
-- rather than the entire history.
CREATE INDEX outbox_unpublished_idx ON outbox (id) WHERE published_at IS NULL;
