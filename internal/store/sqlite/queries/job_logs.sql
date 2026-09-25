-- name: CreateJobLog :one
INSERT INTO job_logs (job_id, attempt, sequence, source, data, created_at) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: ListJobLogs :many
SELECT * FROM job_logs 
WHERE job_id = ? 
AND attempt = ? 
ORDER BY sequence;

