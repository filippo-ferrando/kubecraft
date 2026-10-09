-- name: CreateSession :exec
INSERT INTO sessions (id, user_id, csrf_token, expires_at, ip, user_agent)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetSessionUser :one
SELECT sqlc.embed(sessions), sqlc.embed(users)
FROM sessions JOIN users ON users.id = sessions.user_id
WHERE sessions.id = $1 AND sessions.expires_at > now();

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = $1;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteOtherUserSessions :exec
DELETE FROM sessions WHERE user_id = $1 AND id <> $2;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= now();
