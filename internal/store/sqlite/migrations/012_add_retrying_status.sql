ALTER TABLE job_events RENAME TO job_events_before_retrying_status;
ALTER TABLE jobs RENAME TO jobs_before_retrying_status;

CREATE TABLE jobs (
    id TEXT PRIMARY KEY,
    priority INTEGER NOT NULL,
    gpu_type TEXT NOT NULL DEFAULT '',
    gpus INTEGER NOT NULL CHECK (gpus >= 0),
    cpus INTEGER NOT NULL CHECK (cpus >= 0),
    memory_gb INTEGER NOT NULL CHECK (memory_gb >= 0),
    duration_seconds INTEGER NOT NULL CHECK (duration_seconds >= 0),
    status TEXT NOT NULL CHECK (
        status IN (
            'pending',
            'assigned',
            'running',
            'retrying',
            'succeeded',
            'failed',
            'unschedulable',
            'cancelling',
            'cancelled'
        )
    ),
    node_id TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    lease_expires_at INTEGER NOT NULL DEFAULT 0,
    command TEXT NOT NULL DEFAULT '',
    args TEXT NOT NULL DEFAULT '[]',
    updated_at INTEGER NOT NULL DEFAULT 0,
    image TEXT NOT NULL,
    max_attempts INTEGER NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
    retry_backoff_seconds INTEGER NOT NULL DEFAULT 1 CHECK (retry_backoff_seconds >= 0),
    next_attempt_at INTEGER NOT NULL DEFAULT 0 CHECK (next_attempt_at >= 0),
    last_error TEXT NOT NULL DEFAULT ''
);

INSERT INTO jobs (
    id,
    priority,
    gpu_type,
    gpus,
    cpus,
    memory_gb,
    duration_seconds,
    status,
    node_id,
    attempt,
    lease_expires_at,
    command,
    args,
    updated_at,
    image,
    max_attempts,
    retry_backoff_seconds,
    next_attempt_at,
    last_error
)
SELECT
    id,
    priority,
    gpu_type,
    gpus,
    cpus,
    memory_gb,
    duration_seconds,
    status,
    node_id,
    attempt,
    lease_expires_at,
    command,
    args,
    updated_at,
    image,
    max_attempts,
    retry_backoff_seconds,
    next_attempt_at,
    last_error
FROM jobs_before_retrying_status;

CREATE TABLE job_events (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    attempt INTEGER NOT NULL,
    event TEXT NOT NULL CHECK (
        event IN (
            'submitted',
            'assigned',
            'claimed',
            'completed',
            'failed',
            'retry_scheduled',
            'retry_ready',
            'cancellation_requested',
            'cancelled',
            'lease_expired'
        )
    ),
    node_id TEXT NOT NULL,
    message TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

INSERT INTO job_events (
    id,
    job_id,
    attempt,
    event,
    node_id,
    message,
    created_at
)
SELECT
    id,
    job_id,
    attempt,
    event,
    node_id,
    message,
    created_at
FROM job_events_before_retrying_status;

DROP TABLE job_events_before_retrying_status;
DROP TABLE jobs_before_retrying_status;

CREATE INDEX jobs_status_idx ON jobs (status);
CREATE INDEX jobs_updated_at_idx ON jobs (updated_at DESC, id);
CREATE INDEX jobs_retry_eligibility_idx
ON jobs (status, next_attempt_at, priority DESC, id);
CREATE INDEX job_events_job_attempt_idx
ON job_events (job_id, attempt, created_at);
