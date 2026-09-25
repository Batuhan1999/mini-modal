CREATE TABLE IF NOT EXISTS job_events (
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
        'cancellation_requested',
        'cancelled',
        'lease_expired'
    )
),
    node_id TEXT NOT NULL,
    message TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX job_events_job_attempt_idx
ON job_events (job_id, attempt, created_at);
