-- name: CreatePayment :one
INSERT INTO payments (
    id, order_id, user_id, status, amount_minor, amount_currency, method
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetPayment :one
SELECT * FROM payments WHERE id = $1;

-- name: GetPaymentForUpdate :one
-- Settling reads the attempt and then writes it, and the two writers that meet
-- here are ordinary rather than rare: the provider's callback can arrive before
-- the response to the call that caused it. FOR UPDATE makes Postgres order
-- them, so the second reads the first one's outcome instead of racing it to a
-- version number and losing.
SELECT * FROM payments WHERE id = $1 FOR UPDATE;

-- name: GetPaymentsByOrderIDs :many
-- Every attempt against a batch of orders in one round trip. Looping the
-- single-order query per row is the N+1 this exists to remove.
SELECT * FROM payments
WHERE order_id = ANY(sqlc.arg(order_ids)::uuid[])
ORDER BY order_id, created_at DESC;

-- name: SettlePayment :one
-- The optimistic lock is the WHERE clause: a writer carrying a stale version
-- affects zero rows and finds out, rather than overwriting whatever the other
-- one decided. It is a backstop behind GetPaymentForUpdate rather than the
-- main guard, and it costs nothing to keep.
UPDATE payments
SET status = $3,
    provider_reference = $4,
    failure_reason = $5,
    updated_at = now(),
    version = version + 1
WHERE id = $1 AND version = $2
RETURNING *;
