ALTER TABLE jobs
ADD COLUMN execution_attempts BIGINT NOT NULL DEFAULT 0 CHECK (execution_attempts >= 0);

UPDATE jobs AS job
SET execution_attempts = claimed.claims
FROM (
    SELECT job_id, COUNT(*) AS claims
    FROM job_events
    WHERE event = 'claimed'
    GROUP BY job_id
) AS claimed
WHERE job.id = claimed.job_id;

ALTER TABLE job_events DROP CONSTRAINT job_events_event_check;
ALTER TABLE job_events ADD CONSTRAINT job_events_event_check
CHECK (event IN ('submitted', 'assigned', 'claimed', 'completed', 'attempt_failed', 'failed', 'retry_scheduled', 'retry_ready', 'cancellation_requested', 'cancelled', 'lease_expired', 'assignment_requeued'));
