-- name: CreateJob :one
INSERT INTO jobs (
    id,
    priority,
    gpu_type,
    gpus,
    cpus,
    memory_gb,
    duration_seconds,
    command,
    args,
    image,
    max_attempts,
    retry_backoff_seconds,
    next_attempt_at,
    last_error,
    status,
    updated_at
) VALUES (sqlc.arg(id), sqlc.arg(priority), sqlc.arg(gpu_type), sqlc.arg(gpus), sqlc.arg(cpus), sqlc.arg(memory_gb), sqlc.arg(duration_seconds), sqlc.arg(command), sqlc.arg(args), sqlc.arg(image), sqlc.arg(max_attempts), sqlc.arg(retry_backoff_seconds), sqlc.arg(next_attempt_at), sqlc.arg(last_error), 'pending', CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT))
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: RetryJob :one
UPDATE jobs
SET status = 'retrying',
    node_id = '',
    lease_expires_at = 0,
    next_attempt_at = sqlc.arg(next_attempt_at),
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT),
    last_error = sqlc.arg(last_error)
WHERE id = sqlc.arg(job_id)
  AND attempt = sqlc.arg(assignment_attempt)
  AND status = 'running'
RETURNING *;

-- name: ReleaseDueRetries :many
UPDATE jobs
SET status = 'pending',
    next_attempt_at = 0,
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE status = 'retrying'
  AND next_attempt_at <= sqlc.arg(ready_before)
RETURNING *;

-- name: GetJob :one
SELECT *
FROM jobs
WHERE id = sqlc.arg(id);

-- name: ListJobs :many
SELECT *
FROM jobs
ORDER BY updated_at DESC, id;

-- name: CountJobs :one
SELECT COUNT(*)
FROM jobs;

-- name: ListRecentJobs :many
SELECT *
FROM jobs
ORDER BY updated_at DESC, id
LIMIT sqlc.arg(row_limit)::bigint;

-- name: ListPendingJobs :many
SELECT *
FROM jobs
WHERE status = 'pending'
  AND next_attempt_at <= CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
ORDER BY priority DESC, id;

-- name: ListActiveJobs :many
SELECT *
FROM jobs
WHERE status IN ('assigned', 'running', 'cancelling')
ORDER BY id;

-- name: AssignJob :one
UPDATE jobs
SET status = 'assigned',
    node_id = sqlc.arg(target_node_id),
    attempt = attempt + 1,
    lease_expires_at = sqlc.arg(lease_expires_at),
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE id = sqlc.arg(job_id)
  AND status = 'pending'
RETURNING *;

-- name: MarkJobUnschedulable :execrows
UPDATE jobs
SET status = 'unschedulable',
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE id = sqlc.arg(id)
  AND status = 'pending';

-- name: ClaimJob :one
UPDATE jobs
SET status = 'running',
    lease_expires_at = sqlc.arg(lease_expires_at),
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
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
    lease_expires_at = 0,
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE id = sqlc.arg(job_id)
  AND attempt = sqlc.arg(assignment_attempt)
  AND status = 'running'
RETURNING *;

-- name: FailJob :one
UPDATE jobs
SET status = 'failed',
    lease_expires_at = 0,
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT),
    last_error = sqlc.arg(last_error)
WHERE id = sqlc.arg(job_id)
  AND attempt = sqlc.arg(assignment_attempt)
  AND status = 'running'
RETURNING *;

-- name: RequeueAssignment :one
UPDATE jobs
SET status = 'pending',
    node_id = '',
    lease_expires_at = 0,
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE id = sqlc.arg(job_id)
  AND node_id = sqlc.arg(assignment_node_id)
  AND attempt = sqlc.arg(assignment_attempt)
  AND status IN ('assigned', 'running')
RETURNING *;

-- name: RenewAssignment :execrows
UPDATE jobs
SET lease_expires_at = sqlc.arg(lease_expires_at),
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE id = sqlc.arg(job_id)
  AND node_id = sqlc.arg(assignment_node_id)
  AND attempt = sqlc.arg(assignment_attempt)
  AND status = 'running';

-- name: RetryExpiredAssignments :many
UPDATE jobs
SET status = 'retrying',
    node_id = '',
    lease_expires_at = 0,
    next_attempt_at = sqlc.arg(expired_before) + retry_backoff_seconds * 1000,
    last_error = 'assignment lease expired',
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE status IN ('assigned', 'running')
  AND lease_expires_at <= sqlc.arg(expired_before)
  AND attempt < max_attempts
RETURNING *;

-- name: FailExpiredAssignments :many
UPDATE jobs
SET status = 'failed',
    lease_expires_at = 0,
    last_error = 'assignment lease expired',
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE status IN ('assigned', 'running')
  AND lease_expires_at <= sqlc.arg(expired_before)
  AND attempt >= max_attempts
RETURNING *;

-- name: RequeueUnschedulableJobs :execrows
UPDATE jobs
SET status = 'pending',
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE status = 'unschedulable';



-- name: StartCancelJob :execrows
UPDATE jobs
SET status = 'cancelling',
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE id = sqlc.arg(id)
  AND status = 'running';

-- name: CancelNotRunningJob :execrows
UPDATE jobs
SET status = 'cancelled',
    node_id = '',
    lease_expires_at = 0,
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE id = sqlc.arg(id)
  AND status IN ('pending', 'assigned', 'retrying', 'unschedulable');

-- name: AcknowledgeCancelledJob :one
UPDATE jobs
SET status = 'cancelled',
    node_id = '',
    lease_expires_at = 0,
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE id = sqlc.arg(job_id)
  AND attempt = sqlc.arg(assignment_attempt)
  AND status = 'cancelling'
RETURNING *;

-- name: CancelExpiredAssignments :many
UPDATE jobs
SET status = 'cancelled',
    node_id = '',
    lease_expires_at = 0,
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE status = 'cancelling'
  AND lease_expires_at <= sqlc.arg(expired_before)
RETURNING *;
