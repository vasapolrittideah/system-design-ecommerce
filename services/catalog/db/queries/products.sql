-- name: CreateProduct :one
INSERT INTO products (id, name, description, category, status)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetProductByID :one
SELECT * FROM products
WHERE id = $1;

-- name: GetProductsByIDs :many
-- ANY over one array parameter rather than an IN list built by string
-- concatenation: one prepared statement whatever the batch size, and nothing to
-- escape. Fewer rows than ids is the ordinary case and not an error.
SELECT * FROM products
WHERE id = ANY(@ids::uuid[]);

-- name: UpdateProduct :one
-- The optimistic lock, and the reason this statement guards the whole
-- aggregate: adding a variant changes no column here but still runs this
-- update, so two concurrent writers that both read version 3 produce one
-- success and one zero-row result. The loser's variant insert is rolled back
-- with it, which is what a transaction is for.
--
-- No rows means the version moved, and the repository turns that into a
-- conflict. Checking the version with a preceding SELECT would leave a window
-- between the read and the write where exactly that is not true.
UPDATE products
SET name        = $2,
    description = $3,
    category    = $4,
    status      = $5,
    updated_at  = now(),
    version     = version + 1
WHERE id = $1
  AND version = $6
RETURNING *;

-- name: ListProducts :many
-- Keyset pagination. The cursor is compared as a row so that (created_at, id)
-- is one ordered key: products written in the same transaction share a
-- timestamp, and paging on it alone would drop or repeat whichever of them
-- straddled the boundary.
--
-- A first page passes NULL for both halves and COALESCE turns that into the
-- largest value either column can hold, so there is one predicate rather than
-- two queries — and it stays a predicate the index products_status_created_idx
-- can be scanned with, which an `OR cursor IS NULL` would not.
SELECT * FROM products
WHERE status = $1
  AND (created_at, id) < (
    COALESCE(sqlc.narg(before_created_at)::timestamptz, 'infinity'),
    COALESCE(sqlc.narg(before_id)::uuid, 'ffffffff-ffff-ffff-ffff-ffffffffffff')
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListProductsInCategory :many
-- The same page within one category. A separate statement rather than one more
-- optional predicate, because the two are served by different indexes: a
-- category filter written as `OR $2 IS NULL` cannot be scanned with
-- products_category_created_idx, and would read the whole status partition to
-- find one category's worth of rows.
SELECT * FROM products
WHERE status = $1
  AND category = $2
  AND (created_at, id) < (
    COALESCE(sqlc.narg(before_created_at)::timestamptz, 'infinity'),
    COALESCE(sqlc.narg(before_id)::uuid, 'ffffffff-ffff-ffff-ffff-ffffffffffff')
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);
