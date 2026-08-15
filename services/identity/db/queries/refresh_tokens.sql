-- name: CreateRefreshToken :one
-- revoked_at is left to its NULL default: a token is born live, and there is no
-- caller that wants to store one already spent.
INSERT INTO refresh_tokens (id, user_id, token_hash, family_id, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetRefreshTokenByHash :one
-- The only way a presented token becomes a row. The UNIQUE index on token_hash
-- is what serves this, so there is at most one match by construction.
SELECT * FROM refresh_tokens
WHERE token_hash = $1;

-- name: SpendRefreshToken :execrows
-- Marks a live token spent, and the row count is the answer the caller needs:
-- `revoked_at IS NULL` makes this the whole concurrency guard, so two refreshes
-- racing with the same token produce one success and one zero. Checking first
-- and updating second would let both read a live row and both proceed.
--
-- Deliberately not filtered on version. The transition that has to be safe is
-- live → spent, and this states it directly; an optimistic lock would only say
-- that nobody else had written, which is a weaker claim and a redundant one
-- here.
UPDATE refresh_tokens
SET revoked_at = $2,
    updated_at = now(),
    version = version + 1
WHERE id = $1
  AND revoked_at IS NULL;

-- name: RevokeRefreshTokenFamily :exec
-- Ends a whole rotation chain: what a logout does, and what a token presented
-- twice earns. Already-revoked rows are excluded rather than rewritten, so the
-- timestamp on each one stays the moment it actually stopped working.
UPDATE refresh_tokens
SET revoked_at = $2,
    updated_at = now(),
    version = version + 1
WHERE family_id = $1
  AND revoked_at IS NULL;
