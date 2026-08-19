-- name: CreateStockItem :one
-- The UNIQUE constraint on sku is the guard, not a preceding SELECT. Reading
-- first and inserting second leaves a window in which two requests both find
-- nothing and both proceed; the repository maps the resulting unique violation
-- to a conflict instead.
INSERT INTO stock_items (id, sku, available)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetStockItemBySKU :one
SELECT * FROM stock_items
WHERE sku = $1;

-- name: GetStockItemsBySKUs :many
-- ANY over one array parameter rather than an IN list built by string
-- concatenation: one prepared statement whatever the batch size, and nothing to
-- escape. Fewer rows than SKUs is the ordinary case and not an error — a SKU
-- nobody has stocked yet simply has no row.
SELECT * FROM stock_items
WHERE sku = ANY(@skus::text[]);

-- name: ReserveStock :one
-- The statement this whole service is arranged around.
--
-- `available >= quantity` in the WHERE rather than in Go is what makes
-- overselling impossible under concurrency: two checkouts for the last unit
-- both run this, PostgreSQL serialises them on the row lock, and the second one
-- re-evaluates the predicate against what the first left behind and matches no
-- rows. A read followed by a write cannot do that — between the two, the
-- number the decision was made on is already out of date.
--
-- No rows therefore means "not enough", and the repository turns it into a
-- conflict. It also means "no such SKU", which the repository tells apart with
-- a follow-up read on the failure path only.
UPDATE stock_items
SET available  = available - sqlc.arg(quantity),
    reserved   = reserved + sqlc.arg(quantity),
    updated_at = now(),
    version    = version + 1
WHERE sku = sqlc.arg(sku)
  AND available >= sqlc.arg(quantity)
RETURNING *;

-- name: ReleaseStock :one
-- Gives a hold back. `reserved >= quantity` is the same kind of guard as the
-- one above and exists for a different reason: nothing should ever release more
-- than was held, and if the counts have drifted, this refuses rather than
-- inventing stock that does not exist on a shelf.
UPDATE stock_items
SET available  = available + sqlc.arg(quantity),
    reserved   = reserved - sqlc.arg(quantity),
    updated_at = now(),
    version    = version + 1
WHERE sku = sqlc.arg(sku)
  AND reserved >= sqlc.arg(quantity)
RETURNING *;

-- name: CommitStock :one
-- The goods left the building: the hold disappears and nothing returns to
-- available. This is the only statement here that reduces the total the
-- warehouse holds.
UPDATE stock_items
SET reserved   = reserved - sqlc.arg(quantity),
    updated_at = now(),
    version    = version + 1
WHERE sku = sqlc.arg(sku)
  AND reserved >= sqlc.arg(quantity)
RETURNING *;

-- name: AdjustStock :one
-- A delivery arriving or a breakage written off. The predicate is evaluated by
-- the server against the current row, so two receipts landing together both
-- apply — which an absolute count read and written back would not.
--
-- An adjustment that would take the count below zero matches no rows and is
-- refused. Clamping at zero would leave the number wrong and say nothing.
UPDATE stock_items
SET available  = available + sqlc.arg(delta),
    updated_at = now(),
    version    = version + 1
WHERE sku = sqlc.arg(sku)
  AND available + sqlc.arg(delta) >= 0
RETURNING *;
