ALTER TABLE function_versions
    DROP COLUMN command,
    DROP COLUMN args,
    DROP COLUMN duration_seconds,
    ADD COLUMN entrypoint TEXT NOT NULL CHECK (length(btrim(entrypoint)) > 0),
    ADD COLUMN timeout_seconds BIGINT NOT NULL CHECK (timeout_seconds >= 0);

ALTER TABLE jobs
    DROP COLUMN gpu_type,
    DROP COLUMN gpus,
    DROP COLUMN cpus,
    DROP COLUMN memory_gb,
    DROP COLUMN duration_seconds,
    DROP COLUMN command,
    DROP COLUMN args,
    DROP COLUMN image,
    DROP COLUMN max_attempts,
    DROP COLUMN retry_backoff_seconds,
    ADD COLUMN input_json JSONB NOT NULL CHECK (jsonb_typeof(input_json) = 'object'),
    ADD COLUMN created_at BIGINT NOT NULL;

CREATE TABLE job_results (
    job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
    attempt BIGINT NOT NULL CHECK (attempt > 0),
    output_json JSONB NOT NULL,
    created_at BIGINT NOT NULL
);
