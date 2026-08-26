-- One attempt to collect the money for an order.
--
-- One attempt, not one order. A declined card is a finished attempt and trying
-- again is a new one, so order_id is not unique here — what is constrained is
-- how many attempts may be *unresolved* at once, which is the index below.

-- +goose Up

CREATE TABLE payments (
    -- No DEFAULT gen_random_uuid(). The id is minted before the row exists,
    -- because it is also the idempotency key the provider is given: a retry
    -- after an ambiguous timeout can only reach the same charge if the key
    -- predates the first call.
    id                 uuid        PRIMARY KEY,

    -- The order being collected for. No foreign key: orders live in another
    -- service's database, and a constraint across that boundary is the shared
    -- database this repo exists to avoid.
    order_id           uuid        NOT NULL,

    -- Who is paying, taken from the verified identity on the call. No foreign
    -- key, for the reason order_id has none.
    user_id            uuid        NOT NULL,

    -- text with a CHECK rather than a native enum type: adding a state to an
    -- enum is an ALTER TYPE whose new value cannot be used in the transaction
    -- that adds it, which is exactly the transaction goose wraps a migration
    -- in. Which states exist is stated here; which may follow which is the
    -- domain's state machine and is not expressible in a constraint.
    status             text        NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed')),

    -- What was sent to the provider, copied from the order when the attempt
    -- started. Copied rather than read back later: this is the number the
    -- provider was asked for, and it has to stay readable next to what the
    -- provider says it charged even after the order has moved on.
    --
    -- Strictly positive. A charge for nothing is not a charge, and a provider
    -- asked to make one answers in its own way rather than in ours.
    amount_minor       bigint      NOT NULL CHECK (amount_minor > 0),
    amount_currency    text        NOT NULL CHECK (amount_currency ~ '^[A-Z]{3}$'),

    -- How the customer chose to pay, in this system's vocabulary. Empty means
    -- whatever the provider defaults to, which is what a hosted checkout page
    -- decides for itself — so '' is a value here and not a missing one, and the
    -- column is NOT NULL DEFAULT '' rather than nullable.
    method             text        NOT NULL DEFAULT '' CHECK (method = '' OR method ~ '^[a-z][a-z0-9_]*$'),

    -- What the provider calls this charge. Empty until it has answered, which
    -- is a state that has to be representable: the row is written before the
    -- call so that a call which times out has left a record of itself.
    provider_reference text        NOT NULL DEFAULT '',

    -- The provider's own words, empty unless the attempt failed. Deliberately
    -- unconstrained: the set of these belongs to the provider and changes
    -- without this system being told.
    failure_reason     text        NOT NULL DEFAULT '',

    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),

    -- Optimistic locking. It is a backstop rather than the main guard here:
    -- settling reads the row FOR UPDATE, so the two writers that meet — a
    -- provider callback and the response to the call that caused it — are
    -- ordered by the lock rather than by racing this number.
    version            int         NOT NULL DEFAULT 1
);

-- At most one unresolved attempt per order, and this is the guard that makes a
-- double charge structurally impossible rather than a matter of timing.
--
-- The idempotency key stops a *retry* from charging twice. It does nothing
-- about two genuine submissions — a double-clicked button carrying two keys,
-- or the same order open in two tabs — which would both find the order payable
-- and both ask for money. Letting the database refuse the second is the same
-- move as `UPDATE stock SET available = available - $1 WHERE available >= $1`:
-- the final guard belongs where the write happens, not in a check that read
-- first.
--
-- Failed attempts fall out of the index, which is what lets a customer whose
-- card was declined try another one. A pending attempt does not, and that is
-- deliberate: while it is unresolved nobody knows whether the money moved, and
-- starting a second charge on the strength of not knowing is the double charge
-- this index exists to prevent.
CREATE UNIQUE INDEX payments_one_unresolved_per_order_idx
    ON payments (order_id) WHERE status <> 'failed';

-- Reading every attempt against a batch of orders, which is what fills an order
-- history screen without looping single-item calls.
CREATE INDEX payments_order_idx ON payments (order_id);

-- Finding an attempt from what the provider calls it. Not unique: a reference
-- is the provider's string to choose, and a UNIQUE here would make a provider's
-- collision this service's outage. It is a lookup path for reconciliation.
CREATE INDEX payments_provider_reference_idx
    ON payments (provider_reference) WHERE provider_reference <> '';

-- +goose Down

DROP TABLE payments;
