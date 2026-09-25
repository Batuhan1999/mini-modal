CREATE UNIQUE INDEX IF NOT EXISTS job_logs_job_attempt_sequence_idx
ON job_logs (job_id, attempt, sequence);
