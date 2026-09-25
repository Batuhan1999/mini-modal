-- name: CreateJobLog :one
INSERT INTO job_logs (id, job_id, attempt, sequence, source, data, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (job_id, attempt, sequence) DO UPDATE SET
    source = excluded.source,
    data = excluded.data,
    created_at = excluded.created_at
RETURNING *;

-- name: ListJobLogs :many
SELECT * FROM job_logs 
WHERE job_id = ? 
AND attempt = ? 
ORDER BY sequence;
