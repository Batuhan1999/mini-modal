-- name: UpsertNode :one
INSERT INTO nodes (id, gpu_type, total_gpus, total_cpus, total_memory_gb)
VALUES (sqlc.arg(id), sqlc.arg(gpu_type), sqlc.arg(total_gpus), sqlc.arg(total_cpus), sqlc.arg(total_memory_gb))
ON CONFLICT (id) DO UPDATE SET
    gpu_type = excluded.gpu_type,
    total_gpus = excluded.total_gpus,
    total_cpus = excluded.total_cpus,
    total_memory_gb = excluded.total_memory_gb
RETURNING *;

-- name: GetNode :one
SELECT * FROM nodes WHERE id = sqlc.arg(id);

-- name: ListNodes :many
SELECT * FROM nodes ORDER BY id;
