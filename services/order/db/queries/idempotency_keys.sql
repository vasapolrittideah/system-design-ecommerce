-- name: ClaimIdempotencyKey :one
-- Takes the key for this order, or reports that somebody already has it.
--
-- ON CONFLICT DO NOTHING rather than an upsert: a second claim must not
-- overwrite the first one's request_hash, which is what tells a retry from a
-- key reused for different content. A caller that gets no row back reads the
-- existing claim and decides what to answer.
--
-- Run inside the transaction that writes the order. A concurrent submit blocks
-- here until that transaction ends, and then either finds the committed claim
-- or takes the key the rollback released.
INSERT INTO idempotency_keys (user_id, key, request_hash, order_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, key) DO NOTHING
RETURNING *;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys WHERE user_id = $1 AND key = $2;

-- name: DeleteExpiredIdempotencyKeys :execrows
-- The sweep. Kept 24h: long enough to outlive any retry a client is still
-- making, short enough that the table stays small.
DELETE FROM idempotency_keys WHERE created_at < $1;
