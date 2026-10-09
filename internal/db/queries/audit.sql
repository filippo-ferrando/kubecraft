-- name: InsertAudit :exec
INSERT INTO audit_log (user_id, action, target, details)
VALUES ($1, $2, $3, $4);

-- name: ListAudit :many
SELECT audit_log.*, coalesce(users.username, '')::text AS username
FROM audit_log LEFT JOIN users ON users.id = audit_log.user_id
WHERE (sqlc.narg(before_id)::bigint IS NULL OR audit_log.id < sqlc.narg(before_id))
ORDER BY audit_log.id DESC
LIMIT $1;
