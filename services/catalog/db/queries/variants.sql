-- name: CreateVariant :one
-- The unique constraint on sku is the guard, not a preceding SELECT. Reading
-- first and inserting second leaves a window in which two requests both find
-- nothing and both proceed; the repository maps the resulting unique violation
-- to a conflict instead.
INSERT INTO variants (id, product_id, sku, price_amount_minor, price_currency, attributes)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateVariant :one
-- Price and attributes only. The SKU is what orders, carts, and the warehouse
-- already wrote down, and this statement is deliberately unable to change it.
UPDATE variants
SET price_amount_minor = $2,
    price_currency     = $3,
    attributes         = $4,
    updated_at         = now(),
    version            = version + 1
WHERE id = $1
  AND version = $5
RETURNING *;

-- name: GetVariantsByProductIDs :many
-- Loads the variants for a page of products in one round trip, because the
-- alternative is a query per product and this read serves a listing.
--
-- The ordering is what makes a product's variants arrive in the same sequence
-- every time: created_at alone repeats within a transaction, so the id settles
-- it — a shopper must not see the sizes reshuffle between two page loads.
SELECT * FROM variants
WHERE product_id = ANY(@product_ids::uuid[])
ORDER BY created_at, id;

-- name: GetVariantsBySKUs :many
-- The sellable units a cart names, joined to their product so that only the
-- ones on sale come back.
--
-- The status filter is in the query rather than applied afterwards: a variant
-- of a draft product must not be priced for anybody, and a filter the caller
-- has to remember is one that eventually gets forgotten.
SELECT v.* FROM variants v
JOIN products p ON p.id = v.product_id
WHERE v.sku = ANY(sqlc.arg(skus)::text[])
  AND p.status = sqlc.arg(status)
ORDER BY v.sku;
