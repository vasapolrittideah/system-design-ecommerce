-- A product is what the storefront describes; a variant is what gets bought.
-- This table holds the first half, and owns the lifecycle both halves obey.

-- +goose Up

CREATE TABLE products (
    -- No DEFAULT gen_random_uuid(). The aggregate mints its own id before it
    -- is persisted, because the domain event it raises carries that id and is
    -- written to the outbox in the same transaction as the row.
    id          uuid        PRIMARY KEY,

    name        text        NOT NULL CHECK (btrim(name) <> ''),

    description text        NOT NULL DEFAULT '',

    -- A slug, or empty for uncategorised. Stored already lowercased so a
    -- filter is a plain equality rather than a functional index; the CHECK is
    -- what makes an adapter that forgets to normalise fail loudly instead of
    -- quietly creating a category no query will ever match.
    category    text        NOT NULL DEFAULT '' CHECK (category = lower(category)),

    -- text with a CHECK rather than a native enum type: adding a state to an
    -- enum is an ALTER TYPE whose new value cannot be used in the transaction
    -- that adds it, which is exactly the transaction goose wraps a migration
    -- in. The constraint is here to catch an adapter writing a string nobody
    -- defined — which states exist, and which may follow which, is the
    -- domain's state machine and is not expressible here.
    status      text        NOT NULL CHECK (status IN ('draft', 'active', 'archived')),

    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),

    -- Optimistic locking. Every UPDATE carries the version it read and bumps
    -- it, so two concurrent writers cannot silently overwrite each other —
    -- the loser affects zero rows and finds out.
    version     int         NOT NULL DEFAULT 1
);

-- ListProducts pages by keyset on (created_at, id) rather than by offset, so
-- both indexes carry the tiebreaker: an index that stops at created_at leaves
-- the cursor comparison to a sort, and two products created in the same
-- transaction share a timestamp.
--
-- Two indexes because the filter has two shapes and neither can use the
-- other's: a listing with no category cannot skip the category column to reach
-- the ordering, and one with a category would otherwise scan every product in
-- the requested status.
CREATE INDEX products_status_created_idx ON products (status, created_at DESC, id DESC);

CREATE INDEX products_category_created_idx ON products (status, category, created_at DESC, id DESC);

-- +goose Down

DROP TABLE products;
