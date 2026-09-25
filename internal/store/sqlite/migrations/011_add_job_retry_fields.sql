ALTER TABLE jobs
ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 3
CHECK (max_attempts > 0);

ALTER TABLE jobs
ADD COLUMN retry_backoff_seconds INTEGER NOT NULL DEFAULT 1
CHECK (retry_backoff_seconds >= 0);

ALTER TABLE jobs
ADD COLUMN next_attempt_at INTEGER NOT NULL DEFAULT 0
CHECK (next_attempt_at >= 0);

ALTER TABLE jobs
ADD COLUMN last_error TEXT NOT NULL DEFAULT '';

CREATE INDEX jobs_retry_eligibility_idx
ON jobs (status, next_attempt_at, priority DESC, id);
