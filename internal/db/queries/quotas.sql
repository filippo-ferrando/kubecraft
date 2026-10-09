-- name: EnsureDefaultQuota :exec
INSERT INTO default_quota (id, max_servers) VALUES (true, $1)
ON CONFLICT (id) DO NOTHING;

-- name: GetDefaultQuota :one
SELECT * FROM default_quota WHERE id;

-- name: UpdateDefaultQuota :one
UPDATE default_quota
SET max_servers = $1, max_total_cpu_m = $2, max_total_memory_mb = $3, max_total_storage_gb = $4
WHERE id
RETURNING *;

-- name: GetUserQuota :one
SELECT * FROM user_quotas WHERE user_id = $1;

-- name: UpsertUserQuota :one
INSERT INTO user_quotas (user_id, max_servers, max_total_cpu_m, max_total_memory_mb, max_total_storage_gb)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id) DO UPDATE
SET max_servers = EXCLUDED.max_servers,
    max_total_cpu_m = EXCLUDED.max_total_cpu_m,
    max_total_memory_mb = EXCLUDED.max_total_memory_mb,
    max_total_storage_gb = EXCLUDED.max_total_storage_gb
RETURNING *;

-- name: ListUserQuotas :many
SELECT * FROM user_quotas;

-- name: GetUserUsage :one
SELECT
    count(*)::integer AS servers,
    coalesce(sum(plans.storage_gb), 0)::integer AS storage_gb,
    coalesce(sum(plans.cpu_limit_m) FILTER (WHERE servers.desired_state = 'running'), 0)::integer AS running_cpu_m,
    coalesce(sum(plans.memory_limit_mb) FILTER (WHERE servers.desired_state = 'running'), 0)::integer AS running_memory_mb
FROM servers JOIN plans ON plans.id = servers.plan_id
WHERE servers.owner_id = $1 AND (sqlc.narg(exclude_id)::uuid IS NULL OR servers.id <> sqlc.narg(exclude_id));
