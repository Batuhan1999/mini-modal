-- name: CreateJob :one
INSERT INTO jobs (
    id,
    priority,
    gpu_type,
    gpus,
    cpus,
    memory_gb,
    duration_seconds,
    status
) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending')
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: GetJob :one
SELECT *
FROM jobs
WHERE id = ?;

-- name: ListJobs :many
SELECT *
FROM jobs
ORDER BY id;

-- name: ListPendingJobs :many
SELECT *
FROM jobs
WHERE status = 'pending'
ORDER BY priority DESC, id;

-- name: ListActiveJobs :many
SELECT *
FROM jobs
WHERE status IN ('assigned', 'running')
ORDER BY id;

-- name: AssignJob :one
UPDATE jobs
SET status = 'assigned',
    node_id = sqlc.arg(target_node_id),
    attempt = attempt + 1,
    lease_expires_at = sqlc.arg(lease_expires_at)
WHERE id = sqlc.arg(job_id)
  AND status = 'pending'
RETURNING *;

-- name: MarkJobUnschedulable :execrows
UPDATE jobs
SET status = 'unschedulable'
WHERE id = ?
  AND status = 'pending';

-- name: ClaimJob :one
UPDATE jobs
SET status = 'running',
    lease_expires_at = sqlc.arg(lease_expires_at)
WHERE id = (
    SELECT candidate.id
    FROM jobs AS candidate
    WHERE candidate.node_id = sqlc.arg(claim_node_id)
      AND candidate.status = 'assigned'
    ORDER BY candidate.priority DESC, candidate.id
    LIMIT 1
)
  AND status = 'assigned'
RETURNING *;

-- name: CompleteJob :one
UPDATE jobs
SET status = 'succeeded',
    lease_expires_at = 0
WHERE id = sqlc.arg(job_id)
  AND attempt = sqlc.arg(assignment_attempt)
  AND status = 'running'
RETURNING *;

-- name: FailJob :one
UPDATE jobs
SET status = 'failed',
    lease_expires_at = 0
WHERE id = sqlc.arg(job_id)
  AND attempt = sqlc.arg(assignment_attempt)
  AND status = 'running'
RETURNING *;

-- name: RequeueAssignment :one
UPDATE jobs
SET status = 'pending',
    node_id = '',
    lease_expires_at = 0
WHERE id = sqlc.arg(job_id)
  AND node_id = sqlc.arg(assignment_node_id)
  AND attempt = sqlc.arg(assignment_attempt)
  AND status IN ('assigned', 'running')
RETURNING *;

-- name: RenewAssignment :execrows
UPDATE jobs
SET lease_expires_at = sqlc.arg(lease_expires_at)
WHERE id = sqlc.arg(job_id)
  AND node_id = sqlc.arg(assignment_node_id)
  AND attempt = sqlc.arg(assignment_attempt)
  AND status = 'running';

-- name: RequeueExpiredAssignments :many
UPDATE jobs
SET status = 'pending',
    node_id = '',
    lease_expires_at = 0
WHERE status IN ('assigned', 'running')
  AND lease_expires_at <= sqlc.arg(expired_before)
RETURNING *;

-- name: RequeueUnschedulableJobs :execrows
UPDATE jobs
SET status = 'pending'
WHERE status = 'unschedulable';
