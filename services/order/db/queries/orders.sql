-- name: CreateOrder :one
INSERT INTO orders (
    id, user_id, status, total_amount_minor, total_currency, reservation_id
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: CreateOrderLine :one
INSERT INTO order_lines (
    id, order_id, sku, quantity, unit_price_amount_minor, unit_price_currency
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetOrder :one
SELECT * FROM orders WHERE id = $1;

-- name: GetOrderLines :many
-- Ordered by SKU rather than by insertion, so an order reads the same way
-- every time it is loaded and a test can compare two loads without sorting.
SELECT * FROM order_lines WHERE order_id = $1 ORDER BY sku;

-- name: GetOrderLinesByOrderIDs :many
-- The lines of many orders in one round trip, for a page of them. Looping the
-- single-order query per row is the N+1 this exists to remove.
SELECT * FROM order_lines WHERE order_id = ANY(sqlc.arg(order_ids)::uuid[]) ORDER BY order_id, sku;

-- name: ListOrdersByUser :many
-- Keyset pagination. The cursor is compared as a row so that (created_at, id)
-- is one ordered key: two orders written in the same millisecond share a
-- timestamp, and paging on it alone would drop or repeat whichever of them
-- straddled the boundary.
--
-- A first page passes NULL for both halves and COALESCE turns that into the
-- largest value either column can hold, so there is one predicate rather than
-- two queries — and it stays a predicate the index orders_user_created_idx can
-- be scanned with, which an `OR cursor IS NULL` would not.
SELECT * FROM orders
WHERE user_id = $1
  AND (created_at, id) < (
    COALESCE(sqlc.narg(before_created_at)::timestamptz, 'infinity'),
    COALESCE(sqlc.narg(before_id)::uuid, 'ffffffff-ffff-ffff-ffff-ffffffffffff')
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: UpdateOrderStatus :one
-- The optimistic lock is the WHERE clause: a writer carrying a stale version
-- affects zero rows and finds out, rather than overwriting whatever the other
-- one decided.
UPDATE orders
SET status = $3,
    updated_at = now(),
    version = version + 1
WHERE id = $1 AND version = $2
RETURNING *;
