ALTER TABLE job_events RENAME TO job_events_before_attempt_failed;

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
            'attempt_failed',
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
    original.id,
    original.job_id,
    original.attempt,
    CASE
        WHEN original.event = 'failed' AND EXISTS (
            SELECT 1
            FROM job_events_before_attempt_failed AS retry
            WHERE retry.job_id = original.job_id
              AND retry.attempt = original.attempt
              AND retry.event = 'retry_scheduled'
        ) THEN 'attempt_failed'
        ELSE original.event
    END,
    original.node_id,
    original.message,
    original.created_at
FROM job_events_before_attempt_failed AS original;

DROP TABLE job_events_before_attempt_failed;

CREATE INDEX job_events_job_attempt_idx
ON job_events (job_id, attempt, created_at);
