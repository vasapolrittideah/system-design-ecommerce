-- A purchase and the lines it was placed for. The order is the aggregate, and
-- its lines are part of it.

-- +goose Up

CREATE TABLE orders (
    -- No DEFAULT gen_random_uuid(). The id is minted before the row exists —
    -- before the stock is even reserved, because the hold has to name the order
    -- it is for — and the event the aggregate raises carries it.
    id                 uuid        PRIMARY KEY,

    -- Who placed it, taken from the verified identity on the call. No foreign
    -- key: users live in another service's database, and a constraint across
    -- that boundary is the shared database this repo exists to avoid.
    user_id            uuid        NOT NULL,

    -- text with a CHECK rather than a native enum type: adding a state to an
    -- enum is an ALTER TYPE whose new value cannot be used in the transaction
    -- that adds it, which is exactly the transaction goose wraps a migration
    -- in. Which states exist is stated here; which may follow which is the
    -- domain's state machine and is not expressible in a constraint.
    status             text        NOT NULL CHECK (status IN ('pending_payment', 'paid', 'cancelled')),

    -- What the customer agreed to pay, stored rather than derived on read. It
    -- is the sum of the lines as they were priced at checkout, and a total
    -- recomputed from the catalog's current prices would rewrite the agreement
    -- every time one moved.
    total_amount_minor bigint      NOT NULL CHECK (total_amount_minor >= 0),
    total_currency     text        NOT NULL CHECK (total_currency ~ '^[A-Z]{3}$'),

    -- Inventory's hold on the stock for this order. Opaque here: this service
    -- knows an order has a hold and that the hold expires, not what a
    -- reservation is made of.
    reservation_id     uuid        NOT NULL,

    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),

    -- Optimistic locking. This row is read before it is written — every saga
    -- step loads the aggregate to ask its state machine — so the version is
    -- what stops a payment and a cancellation arriving together from both
    -- succeeding.
    version            int         NOT NULL DEFAULT 1
);

-- One live hold per order is inventory's rule, enforced there. What this index
-- holds is the other direction: two orders must never share a reservation, or
-- committing one would commit the other's stock.
CREATE UNIQUE INDEX orders_reservation_idx ON orders (reservation_id);

-- The customer's own list, newest first. (created_at, id) is one ordered key
-- because two orders written in the same millisecond share a timestamp, and
-- paging on it alone would drop or repeat whichever of them straddled a page
-- boundary.
CREATE INDEX orders_user_created_idx ON orders (user_id, created_at DESC, id DESC);

CREATE TABLE order_lines (
    id                      uuid        PRIMARY KEY,

    order_id                uuid        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,

    -- No foreign key to a catalog table, and there could not be one: the
    -- catalog is another service's database. A line is a fact about what was
    -- bought, and it has to stay readable after the SKU is retired.
    sku                     text        NOT NULL CHECK (sku = upper(sku)),

    quantity                int         NOT NULL CHECK (quantity > 0),

    -- The price agreed for one, frozen at checkout. This is the column that
    -- makes an order a record of an agreement rather than a view over today's
    -- catalog.
    unit_price_amount_minor bigint      NOT NULL CHECK (unit_price_amount_minor >= 0),
    unit_price_currency     text        NOT NULL CHECK (unit_price_currency ~ '^[A-Z]{3}$'),

    created_at              timestamptz NOT NULL DEFAULT now(),

    -- No updated_at and no version, unlike every other table here. A line is
    -- written once with its order and never modified: changing what was bought
    -- would mean changing what the customer agreed to, and the way to buy
    -- something else is a different order.

    -- One line per SKU. The aggregate refuses a caller's repeated SKUs rather
    -- than summing them, and this is what says so in the schema rather than
    -- only in Go.
    UNIQUE (order_id, sku)
);

-- +goose Down

DROP TABLE order_lines;
DROP TABLE orders;
