-- The inbox is how this service's own consumer of its order events refuses to
-- call inventory a second time for the same delivery: it claims an event's id
-- before doing the work, in the transaction that does the work, so a
-- redelivery finds the claim already taken.
--
-- Copied from pkg/inbox/schema.sql, which is the definition. Every service
-- owns its own database, so the table is duplicated per service rather than
-- shared — but the columns must match what pkg/inbox writes and claims, and
-- editing one side alone breaks the other at runtime rather than at compile
-- time.

-- +goose Up

CREATE TABLE processed_events (
    -- Keyed by the consumer group as well as the event, because two groups
    -- both handling one event is the ordinary case even though this service
    -- has only one today.
    consumer_group text        NOT NULL,

    -- The envelope's event_id, held as text for the same reason the outbox
    -- holds aggregate_id that way: nothing here interprets it.
    event_id       text        NOT NULL,

    processed_at   timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (consumer_group, event_id)
);

-- +goose Down

DROP TABLE processed_events;
