-- The outbox is how this service announces that something happened: the
-- aggregate and the events describing it are written in the same transaction,
-- and pkg/outbox's relay publishes the rows to Kafka afterwards.
--
-- Copied from pkg/outbox/schema.sql, which is the definition. Every service
-- owns its own database, so the table is duplicated per service rather than
-- shared — but the columns must match what pkg/outbox writes and claims, and
-- editing one side alone breaks the other at runtime rather than at compile
-- time.

-- +goose Up

CREATE TABLE outbox (
    -- An identity column, not the uuid every other table carries: the relay
    -- publishes in insertion order and a uuid supplies none. There is no
    -- version column either, because a row is written once and only ever
    -- updated to record that it was published.
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    -- Which aggregate the event came from. aggregate_id doubles as the Kafka
    -- message key, which is what keeps events for one product in order.
    aggregate_type text        NOT NULL,
    aggregate_id   text        NOT NULL,

    -- The event name as consumers know it, e.g. ProductPublished.
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

-- +goose Down

DROP TABLE outbox;
