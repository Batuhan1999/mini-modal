-- name: UpsertNode :one
INSERT INTO nodes (
    id,
    gpu_type,
    total_gpus,
    total_cpus,
    total_memory_gb
) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    gpu_type = excluded.gpu_type,
    total_gpus = excluded.total_gpus,
    total_cpus = excluded.total_cpus,
    total_memory_gb = excluded.total_memory_gb
RETURNING *;

-- name: GetNode :one
SELECT *
FROM nodes
WHERE id = ?;

-- name: ListNodes :many
SELECT *
FROM nodes
ORDER BY id;
