-- name: CreateJobEvent :one
INSERT INTO job_events (id, job_id, attempt, event, node_id, message, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListJobEvents :many
SELECT * FROM job_events
WHERE job_id = ?
AND attempt = ?
ORDER BY created_at, rowid;
