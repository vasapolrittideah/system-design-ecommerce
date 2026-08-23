-- The inbox is how a consumer refuses to do the same work twice: it claims an
-- event's ID before handling it, in the transaction that handles it, so a
-- redelivery finds the claim already taken and skips the work.
--
-- Kafka delivery is at-least-once — a consumer that processed a batch and died
-- before committing its offset reads the batch again — so this table is what
-- stands between one OrderPaid and two shipments.
--
-- Every service owns its own database, so this table is copied into each
-- service's own migrations under services/<name>/db/migrations/ rather than
-- living in one shared schema.

CREATE TABLE processed_events (
    -- Keyed by the consumer group as well as the event, because two groups
    -- both handling one event is the ordinary case: shipping and notification
    -- each want their own copy of OrderPaid, and keying on the event alone
    -- would let whichever consumed first silently suppress the other.
    consumer_group text        NOT NULL,

    -- The envelope's event_id, held as text for the same reason the outbox
    -- holds aggregate_id that way: nothing here interprets it, and a column
    -- that insists on a UUID is a column that decides for the producer.
    event_id       text        NOT NULL,

    processed_at   timestamptz NOT NULL DEFAULT now(),

    -- The claim is the key. There is no surrogate id, because a second unique
    -- column would only be a second way to name the same row, and no version
    -- column, because a row is written once and never updated -- the whole
    -- content of a claim is that it exists.
    PRIMARY KEY (consumer_group, event_id)
);

-- Rows accumulate for as long as the service runs and nothing here deletes
-- them. A service that decides to sweep must keep them longer than the
-- retention of the topics it consumes: an event redelivered after its claim was
-- deleted is an event processed twice, which is the one thing this table exists
-- to prevent.
