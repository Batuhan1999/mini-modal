-- name: CreateJobEvent :one
INSERT INTO job_events (id, job_id, attempt, event, node_id, message, created_at)
VALUES (sqlc.arg(id), sqlc.arg(job_id), sqlc.arg(attempt), sqlc.arg(event), sqlc.arg(node_id), sqlc.arg(message), sqlc.arg(created_at))
RETURNING *;

-- name: ListJobEvents :many
SELECT * FROM job_events
WHERE job_id = sqlc.arg(job_id)
  AND attempt = sqlc.arg(attempt)
ORDER BY created_at, event_seq;

-- name: ListAllJobEvents :many
SELECT * FROM job_events
WHERE job_id = sqlc.arg(job_id)
ORDER BY created_at, event_seq;
