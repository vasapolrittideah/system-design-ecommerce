-- How many of each sellable unit exist, and how many an order has already
-- spoken for. One row per SKU, and the row every checkout in the system
-- contends on.

-- +goose Up

CREATE TABLE stock_items (
    id         uuid        PRIMARY KEY,

    -- The natural key, and what every query here looks a row up by. Stored
    -- already uppercased so uniqueness never depends on how it was typed; the
    -- CHECK fails loudly on a writer that skipped the domain type instead of
    -- quietly creating a second SKU no human could tell apart.
    sku        text        NOT NULL UNIQUE CHECK (sku = upper(sku)),

    -- What a new reservation may still take.
    --
    -- The CHECK is the last line of the rule this service exists to protect.
    -- The predicate in the reserving UPDATE is what normally enforces it, and
    -- this is what catches the day somebody writes a statement without one:
    -- an oversell becomes a failed write here rather than a negative number
    -- nobody notices until stocktaking.
    available  int         NOT NULL CHECK (available >= 0),

    -- What live reservations are holding. It returns to available when a
    -- reservation is released or expires, and simply disappears when one is
    -- committed, because the goods left the building.
    reserved   int         NOT NULL DEFAULT 0 CHECK (reserved >= 0),

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- Carried for consistency with every other table, and deliberately not the
    -- guard on this one. Reserving is a conditional UPDATE rather than a read
    -- and a write, so there is no version read earlier to compare against —
    -- optimistic locking here would turn contention on a popular SKU into a
    -- retry loop, which is exactly the case that has to work.
    version    int         NOT NULL DEFAULT 1
);

-- +goose Down

DROP TABLE stock_items;
