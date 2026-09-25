ALTER TABLE jobs
ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0;

UPDATE jobs
SET updated_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)
WHERE updated_at = 0;

CREATE INDEX IF NOT EXISTS jobs_updated_at_idx
ON jobs (updated_at DESC, id);
