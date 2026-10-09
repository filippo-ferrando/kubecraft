-- name: CreateServer :one
INSERT INTO servers (owner_id, slug, display_name, plan_id, type, version, settings, desired_state)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetServer :one
SELECT * FROM servers WHERE id = $1;

-- name: GetServerBySlug :one
SELECT * FROM servers WHERE slug = $1;

-- name: SlugExists :one
SELECT EXISTS (SELECT 1 FROM servers WHERE slug = $1);

-- name: ListServers :many
SELECT * FROM servers ORDER BY created_at;

-- name: ListServersByOwner :many
SELECT * FROM servers WHERE owner_id = $1 ORDER BY created_at;

-- name: CountServersByOwner :one
SELECT count(*) FROM servers WHERE owner_id = $1;

-- name: SetDesiredState :one
UPDATE servers SET desired_state = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateServerConfig :one
UPDATE servers SET display_name = $2, plan_id = $3, version = $4, settings = $5, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetObservedState :exec
UPDATE servers SET observed_state = $2, status_message = $3
WHERE id = $1 AND (observed_state <> $2 OR status_message <> $3);

-- name: MarkServerDeleting :one
UPDATE servers SET desired_state = 'deleting', keep_data = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteServer :exec
DELETE FROM servers WHERE id = $1;
