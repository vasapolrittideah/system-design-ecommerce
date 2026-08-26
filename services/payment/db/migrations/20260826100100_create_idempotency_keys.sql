-- The client's own key for one submission, so that a retry after an ambiguous
-- timeout returns the first answer instead of asking a provider for money a
-- second time.
--
-- A table here rather than a key in Redis, and that is the whole point of it:
-- claiming the key and writing the attempt have to be one act. A claim that
-- commits against a write that rolls back would answer the client's retry with
-- a replayed response for a charge that was never made, and nothing outside
-- this database can join the transaction that prevents it.
--
-- It deduplicates the submit and not the order. A customer whose card was
-- declined and who tries again sends a new key and gets a new attempt, which is
-- the behaviour this table is meant to allow rather than prevent — what stops
-- two *unresolved* attempts is the partial unique index on payments.

-- +goose Up

CREATE TABLE idempotency_keys (
    -- Scoped to the user, so two clients that both send "pay-1" are two claims
    -- rather than one collision.
    user_id      uuid        NOT NULL,
    key          text        NOT NULL,

    -- What the key was first used for. A retry carrying the same key and a
    -- different body is a client bug, not a retry, and answering it with the
    -- first attempt would be answering a question nobody asked.
    request_hash bytea       NOT NULL,

    -- The answer. It is the attempt's id rather than a serialized response:
    -- replaying means loading the attempt, which keeps one copy of it in the
    -- database and answers a retry with the attempt as it is now rather than as
    -- it was at the instant it was created — which matters more here than
    -- anywhere else, since the provider may have settled it in between.
    --
    -- NOT NULL, and no state column beside it. There is no in-flight row to
    -- observe: the claim is written in the transaction that writes the attempt,
    -- so a second submit arriving meanwhile blocks on this primary key until
    -- the first commits and then reads the answer — or the first rolled back,
    -- taking the row with it, and the second claims the key itself. Postgres
    -- does the waiting that a state column would otherwise have to describe.
    --
    -- No foreign key to payments, though the row it names is written in the
    -- same transaction. A constraint would have to be DEFERRABLE for the claim
    -- to be taken before the attempt exists, and the attempt it points at is
    -- never deleted — attempts fail, they are not removed.
    payment_id   uuid        NOT NULL,

    created_at   timestamptz NOT NULL DEFAULT now(),

    -- No updated_at and no version: the row is written once, by the
    -- transaction that claimed it, and nothing ever modifies it.

    PRIMARY KEY (user_id, key)
);

-- What a sweep runs against. The claim is kept 24h — long enough to outlive
-- any retry a client is still making, short enough that the table stays small.
CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at);

-- +goose Down

DROP TABLE idempotency_keys;
