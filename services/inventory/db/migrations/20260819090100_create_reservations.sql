-- An order's claim on stock: held for a while, then committed or given back.
-- The reservation is the aggregate, and its lines are part of it.

-- +goose Up

CREATE TABLE reservations (
    -- No DEFAULT gen_random_uuid(). The aggregate mints its own id before it
    -- is persisted, because the domain event it raises carries that id and is
    -- written to the outbox in the same transaction as the row.
    id         uuid        PRIMARY KEY,

    -- The order this was taken for, and the idempotency key. One *live* hold
    -- per order, enforced by the partial unique index below rather than by a
    -- column constraint — see it for why.
    order_id   uuid        NOT NULL,

    -- text with a CHECK rather than a native enum type: adding a state to an
    -- enum is an ALTER TYPE whose new value cannot be used in the transaction
    -- that adds it, which is exactly the transaction goose wraps a migration
    -- in. Which states exist is stated here; which may follow which is the
    -- domain's state machine and is not expressible in a constraint.
    status     text        NOT NULL CHECK (status IN ('held', 'committed', 'released', 'expired')),

    -- When a held reservation stops being honoured. Not nullable: a hold with
    -- no expiry is the stranded reservation this column exists to prevent.
    expires_at timestamptz NOT NULL,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- Optimistic locking. Unlike stock_items this row is read before it is
    -- written — committing and releasing both load the aggregate to ask its
    -- state machine — so the version is what stops a commit and a release
    -- arriving together from both succeeding.
    version    int         NOT NULL DEFAULT 1
);

-- One held reservation per order, and any number of finished ones.
--
-- This is what makes reserving idempotent: a saga retrying after an ambiguous
-- timeout finds its own hold instead of taking a second one on the same goods,
-- and two retries racing produce one insert and one refusal rather than two
-- holds.
--
-- Partial rather than a plain UNIQUE on the column, because an order whose hold
-- expired before it was paid for has to be able to reserve again. A constraint
-- over every row would make that impossible and leave the order permanently
-- unfillable, while still keeping none of the history this index preserves.
CREATE UNIQUE INDEX reservations_held_order_idx ON reservations (order_id) WHERE status = 'held';

-- What the reaper scans. Partial, because once the service has been running a
-- day the held rows are a vanishing fraction of the table and an index over
-- all of them would be mostly history nobody sweeps.
CREATE INDEX reservations_expiring_idx ON reservations (expires_at) WHERE status = 'held';

CREATE TABLE reservation_lines (
    id             uuid        PRIMARY KEY,

    reservation_id uuid        NOT NULL REFERENCES reservations (id) ON DELETE CASCADE,

    -- No foreign key to stock_items. A line is a fact about what was held, and
    -- it has to stay readable after a SKU is retired from the catalogue —
    -- pointing at a row somebody may delete is how an order's own history
    -- disappears.
    sku            text        NOT NULL CHECK (sku = upper(sku)),

    quantity       int         NOT NULL CHECK (quantity > 0),

    created_at     timestamptz NOT NULL DEFAULT now(),

    -- No updated_at and no version, unlike every other table here. A line is
    -- written once with its reservation and never modified: changing what was
    -- held would mean changing what the stock counts already recorded, and the
    -- way to hold something else is a different reservation.

    -- One line per SKU. The aggregate sums a caller's repeated lines before it
    -- gets here, and this is what says so in the schema rather than only in Go.
    UNIQUE (reservation_id, sku)
);

-- +goose Down

DROP TABLE reservation_lines;

DROP TABLE reservations;
