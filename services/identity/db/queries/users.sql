-- name: CreateUser :one
-- The unique constraint on email is the guard, not a preceding SELECT. Reading
-- first and inserting second leaves a window in which two requests both find
-- nothing and both proceed; the repository maps the resulting unique violation
-- to a conflict instead.
INSERT INTO users (id, email, password_hash, roles)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1;

-- name: GetUsersByIDs :many
-- ANY over one array parameter rather than an IN list built by string
-- concatenation: one prepared statement whatever the batch size, and nothing
-- to escape. Fewer rows than ids is the ordinary case and not an error.
SELECT * FROM users
WHERE id = ANY(@ids::uuid[]);
