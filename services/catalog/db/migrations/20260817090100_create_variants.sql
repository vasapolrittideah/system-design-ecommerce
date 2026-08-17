-- Variants are the units that are actually sold. Cart, order, and eventually
-- the warehouse all reference a row in this table, which is why nothing here
-- is ever deleted once a product has been published.

-- +goose Up

CREATE TABLE variants (
    id                 uuid        PRIMARY KEY,

    -- A foreign key rather than an application-level check because both tables
    -- are this service's own — the rule about never reaching into another
    -- service's data is about other services, not about giving up referential
    -- integrity inside one database.
    --
    -- ON DELETE CASCADE covers the only deletion this service performs, a
    -- draft discarded before it was ever published. An active product is
    -- archived instead, because an order placed last year still names these
    -- rows.
    product_id         uuid        NOT NULL REFERENCES products (id) ON DELETE CASCADE,

    -- Unique across the service, and stored already uppercased so that
    -- uniqueness cannot depend on how it was typed. UNIQUE does double duty:
    -- it is the constraint, and it is the index a lookup by SKU reads through.
    sku                text        NOT NULL UNIQUE CHECK (sku = upper(sku)),

    -- Money is two columns rather than a composite type or a jsonb blob: the
    -- amount is arithmetic and wants to be an integer the planner understands,
    -- and the currency is a code. Minor units — 1050 is THB 10.50 — so no
    -- price in this system is ever a binary float.
    price_amount_minor bigint      NOT NULL CHECK (price_amount_minor >= 0),
    price_currency     text        NOT NULL CHECK (price_currency ~ '^[A-Z]{3}$'),

    -- What distinguishes this variant from its siblings. jsonb and not columns
    -- because no rule in this service reads a key, which is what lets the
    -- catalog sell shirts and rice without knowing which it is doing.
    attributes         jsonb       NOT NULL DEFAULT '{}'::jsonb,

    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    version            int         NOT NULL DEFAULT 1
);

-- Loading an aggregate reads every variant of one product, and it is what the
-- cascade above walks.
CREATE INDEX variants_product_id_idx ON variants (product_id);

-- "Every variant of one product is priced in one currency" is deliberately not
-- enforced here. Holding it would mean the currency living on the product row,
-- and a draft is allowed to exist before anyone has decided what it costs — so
-- the column would be nullable and the constraint would only apply to products
-- that had already satisfied it. The invariant is the domain's, and the
-- aggregate is loaded whole before any variant is written.

-- +goose Down

DROP TABLE variants;
