-- name: CountPlans :one
SELECT count(*) FROM plans;

-- name: CreatePlan :one
INSERT INTO plans (name, cpu_request_m, cpu_limit_m, memory_limit_mb, storage_gb, active)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetPlan :one
SELECT * FROM plans WHERE id = $1;

-- name: ListPlans :many
SELECT * FROM plans ORDER BY memory_limit_mb, name;

-- name: ListActivePlans :many
SELECT * FROM plans WHERE active ORDER BY memory_limit_mb, name;

-- name: UpdatePlan :one
UPDATE plans
SET name = $2, cpu_request_m = $3, cpu_limit_m = $4, memory_limit_mb = $5, storage_gb = $6, active = $7
WHERE id = $1
RETURNING *;

-- name: DeletePlan :exec
DELETE FROM plans WHERE id = $1;

-- name: CountServersOnPlan :one
SELECT count(*) FROM servers WHERE plan_id = $1;
