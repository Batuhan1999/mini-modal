ALTER TABLE jobs RENAME TO jobs_before_cancellation_statuses;

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
    image TEXT NOT NULL
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
    image
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
    image
FROM jobs_before_cancellation_statuses;

DROP TABLE jobs_before_cancellation_statuses;

CREATE INDEX jobs_status_idx ON jobs (status);

CREATE INDEX jobs_updated_at_idx
ON jobs (updated_at DESC, id);
