-- name: CreateNodePool :one
INSERT INTO node_pools (
    id, gpu_type, gpus_per_node, cpus_per_node, memory_gb_per_node,
    min_nodes, max_nodes, desired_nodes, idle_timeout_seconds, autoscale_enabled
) VALUES (
    sqlc.arg(id), sqlc.arg(gpu_type), sqlc.arg(gpus_per_node),
    sqlc.arg(cpus_per_node), sqlc.arg(memory_gb_per_node),
    sqlc.arg(min_nodes), sqlc.arg(max_nodes), sqlc.arg(desired_nodes),
    sqlc.arg(idle_timeout_seconds), sqlc.arg(autoscale_enabled)
)
RETURNING *;

-- name: EnsureLocalNodePool :exec
INSERT INTO node_pools (
    id, gpu_type, gpus_per_node, cpus_per_node, memory_gb_per_node,
    min_nodes, max_nodes, desired_nodes, idle_timeout_seconds, autoscale_enabled
) VALUES ('local-dev', '', 0, 8, 16, 0, 32, 0, 5, TRUE)
ON CONFLICT (id) DO NOTHING;

-- name: UpsertNodePool :one
INSERT INTO node_pools (
    id, gpu_type, gpus_per_node, cpus_per_node, memory_gb_per_node,
    min_nodes, max_nodes, desired_nodes, idle_timeout_seconds, autoscale_enabled
) VALUES (
    sqlc.arg(id), sqlc.arg(gpu_type), sqlc.arg(gpus_per_node),
    sqlc.arg(cpus_per_node), sqlc.arg(memory_gb_per_node),
    sqlc.arg(min_nodes), sqlc.arg(max_nodes), sqlc.arg(desired_nodes),
    sqlc.arg(idle_timeout_seconds), sqlc.arg(autoscale_enabled)
)
ON CONFLICT (id) DO UPDATE SET
    gpu_type = excluded.gpu_type,
    gpus_per_node = excluded.gpus_per_node,
    cpus_per_node = excluded.cpus_per_node,
    memory_gb_per_node = excluded.memory_gb_per_node,
    min_nodes = excluded.min_nodes,
    max_nodes = excluded.max_nodes,
    desired_nodes = excluded.desired_nodes,
    idle_timeout_seconds = excluded.idle_timeout_seconds,
    autoscale_enabled = excluded.autoscale_enabled
RETURNING *;

-- name: GetNodePool :one
SELECT * FROM node_pools WHERE id = sqlc.arg(id);

-- name: ListNodePools :many
SELECT * FROM node_pools ORDER BY id;

-- name: ListAutoscalingNodePools :many
SELECT * FROM node_pools WHERE autoscale_enabled = TRUE ORDER BY id;

-- name: UpdateDesiredNodes :one
UPDATE node_pools
SET desired_nodes = sqlc.arg(desired_nodes)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetDesiredNodesIfChanged :execrows
UPDATE node_pools
SET desired_nodes = sqlc.arg(desired_nodes)
WHERE id = sqlc.arg(id)
  AND autoscale_enabled = TRUE
  AND desired_nodes IS DISTINCT FROM sqlc.arg(desired_nodes);
