-- name: CreateFunction :exec
INSERT INTO functions (id, created_at)
VALUES (sqlc.arg(id), sqlc.arg(created_at))
ON CONFLICT (id) DO NOTHING;

-- name: CreateFunctionVersion :one
INSERT INTO function_versions (
    id, function_id, artifact_key, image, entrypoint,
    gpu_type, gpus, cpus, memory_gb, timeout_seconds,
    max_attempts, retry_backoff_seconds, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(function_id), sqlc.arg(artifact_key),
    sqlc.arg(image), sqlc.arg(entrypoint),
    sqlc.arg(gpu_type), sqlc.arg(gpus), sqlc.arg(cpus),
    sqlc.arg(memory_gb), sqlc.arg(timeout_seconds),
    sqlc.arg(max_attempts), sqlc.arg(retry_backoff_seconds),
    sqlc.arg(created_at)
)
RETURNING *;

-- name: GetFunctionVersion :one
SELECT * FROM function_versions WHERE id = sqlc.arg(id);

-- name: ListFunctions :many
SELECT f.id, f.created_at,
       COUNT(v.id)::BIGINT AS version_count,
       COALESCE(MAX(v.created_at), f.created_at)::BIGINT AS updated_at
FROM functions AS f
LEFT JOIN function_versions AS v ON v.function_id = f.id
GROUP BY f.id
ORDER BY updated_at DESC, f.id
LIMIT 100;

-- name: ListFunctionVersions :many
SELECT * FROM function_versions
WHERE function_id = sqlc.arg(function_id)
ORDER BY created_at DESC, id DESC;
