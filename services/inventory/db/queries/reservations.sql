-- name: CreateReservation :one
-- The UNIQUE constraint on order_id is what makes reserving idempotent under a
-- race: two retries of the same saga step both find no existing hold and both
-- insert, and the loser is told so rather than taking a second hold.
INSERT INTO reservations (id, order_id, status, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CreateReservationLine :one
INSERT INTO reservation_lines (id, reservation_id, sku, quantity)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetReservationByID :one
SELECT * FROM reservations
WHERE id = $1;

-- name: GetHeldReservationByOrderID :one
-- The idempotency lookup. A saga that timed out mid-call asks this before it
-- decides whether to reserve again.
--
-- Held only, matching the partial unique index that makes it single-valued. A
-- reservation that expired or was compensated is history: an order that meets
-- one is not holding anything and has to reserve again.
SELECT * FROM reservations
WHERE order_id = $1
  AND status = 'held';

-- name: GetReservationLinesByReservationIDs :many
-- Loads the lines for a set of reservations in one round trip, because the
-- reaper works on a batch and a query per row would make the sweep cost scale
-- with how far behind it is.
--
-- Ordered by SKU, which is the same order the reserving path takes its row
-- locks in — see the repository. Two sweepers releasing overlapping
-- reservations therefore contend rather than deadlock.
SELECT * FROM reservation_lines
WHERE reservation_id = ANY(@reservation_ids::uuid[])
ORDER BY reservation_id, sku;

-- name: UpdateReservationStatus :one
-- The optimistic lock. A commit and a release arriving together both read
-- version 3; one wins and the other affects no rows and is told to re-read,
-- rather than a sale being quietly undone.
UPDATE reservations
SET status     = $2,
    updated_at = now(),
    version    = version + 1
WHERE id = $1
  AND version = $3
RETURNING *;

-- name: ClaimExpiredReservations :many
-- What the reaper sweeps: holds nobody committed in time.
--
-- FOR UPDATE SKIP LOCKED rather than a plain SELECT, so that two reapers — a
-- rolling restart briefly runs two — divide the backlog instead of fighting
-- over the head of it. The same reason pkg/outbox's relay claims its rows this
-- way, and the rows stay locked until the transaction that returns the stock
-- commits.
--
-- `now` is a parameter rather than now(), so a test can sweep without waiting
-- out a TTL, and so the time the domain judges expiry by is the time this
-- statement selected on.
SELECT * FROM reservations
WHERE status = 'held'
  AND expires_at <= sqlc.arg(now)
ORDER BY expires_at
LIMIT sqlc.arg(row_limit)
FOR UPDATE SKIP LOCKED;
