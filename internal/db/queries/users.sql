-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: CreateUser :one
INSERT INTO users (username, password_hash, role, must_change_password)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY username;

-- name: UpdatePassword :exec
UPDATE users SET password_hash = $2, must_change_password = $3 WHERE id = $1;

-- name: UpdateUserFlags :one
UPDATE users SET role = $2, disabled = $3, console_enabled = $4
WHERE id = $1
RETURNING *;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: LockUser :one
SELECT id FROM users WHERE id = $1 FOR UPDATE;

-- name: AdvisoryXactLock :exec
SELECT pg_advisory_xact_lock($1);
