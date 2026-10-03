-- name: CreateJob :one
INSERT INTO jobs (
    id,
    priority,
    function_version_id,
    input_json,
    status,
    created_at,
    updated_at
) VALUES (sqlc.arg(id), sqlc.arg(priority), sqlc.arg(function_version_id), sqlc.arg(input_json), 'pending', CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT), CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT))
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
SELECT j.id, j.priority, v.gpu_type, v.gpus, v.cpus, v.memory_gb
FROM jobs AS j
JOIN function_versions AS v ON v.id = j.function_version_id
WHERE j.status = 'pending'
  AND j.next_attempt_at <= CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
ORDER BY j.priority DESC, j.id;

-- name: ListCapacityDemand :many
SELECT j.id, j.priority, v.gpu_type, v.gpus, v.cpus, v.memory_gb
FROM jobs AS j
JOIN function_versions AS v ON v.id = j.function_version_id
WHERE j.status IN ('pending', 'unschedulable')
  AND j.next_attempt_at <= CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
ORDER BY j.priority DESC, j.id;

-- name: ListActiveJobs :many
SELECT j.id, j.node_id, j.status, j.lease_expires_at, v.gpu_type, v.gpus, v.cpus, v.memory_gb
FROM jobs AS j
JOIN function_versions AS v ON v.id = j.function_version_id
WHERE j.status IN ('assigned', 'running', 'cancelling')
ORDER BY j.id;

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
    execution_attempts = execution_attempts + 1,
    lease_expires_at = sqlc.arg(lease_expires_at),
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE id = (
    SELECT candidate.id
    FROM jobs AS candidate
    LEFT JOIN nodes node ON node.id = candidate.node_id
    WHERE candidate.node_id = sqlc.arg(claim_node_id)
      AND candidate.status = 'assigned'
      AND (node.should_drain = FALSE)
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

-- name: RequeueExpiredUnclaimedAssignments :many
UPDATE jobs
SET status = 'pending',
    node_id = '',
    lease_expires_at = 0,
    next_attempt_at = 0,
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE status = 'assigned'
  AND lease_expires_at <= sqlc.arg(expired_before)
RETURNING *;

-- name: RequeueAssignedJobsForNode :many
UPDATE jobs
SET status = 'pending',
    node_id = '',
    lease_expires_at = 0,
    next_attempt_at = 0,
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
WHERE status = 'assigned'
  AND node_id = sqlc.arg(node_id)
RETURNING *;

-- name: RetryExpiredAssignments :many
UPDATE jobs AS j
SET status = 'retrying',
    node_id = '',
    lease_expires_at = 0,
    next_attempt_at = sqlc.arg(expired_before) + v.retry_backoff_seconds * 1000,
    last_error = 'assignment lease expired',
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
FROM function_versions AS v
WHERE j.function_version_id = v.id
  AND j.status = 'running'
  AND j.lease_expires_at <= sqlc.arg(expired_before)
  AND j.execution_attempts < v.max_attempts
RETURNING j.*;

-- name: FailExpiredAssignments :many
UPDATE jobs AS j
SET status = 'failed',
    lease_expires_at = 0,
    last_error = 'assignment lease expired',
    updated_at = CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)
FROM function_versions AS v
WHERE j.function_version_id = v.id
  AND j.status = 'running'
  AND j.lease_expires_at <= sqlc.arg(expired_before)
  AND j.execution_attempts >= v.max_attempts
RETURNING j.*;

-- name: CreateJobResult :exec
INSERT INTO job_results (job_id, attempt, output_json, created_at)
VALUES (sqlc.arg(job_id), sqlc.arg(attempt), sqlc.arg(output_json), CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT));

-- name: GetJobResult :one
SELECT * FROM job_results WHERE job_id = sqlc.arg(job_id);

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
