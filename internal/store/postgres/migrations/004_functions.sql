CREATE TABLE functions (
    id TEXT PRIMARY KEY,
    created_at BIGINT NOT NULL
);

CREATE TABLE function_versions (
    id TEXT PRIMARY KEY,
    function_id TEXT NOT NULL REFERENCES functions(id),
    artifact_key TEXT NOT NULL CHECK (length(trim(artifact_key)) > 0),
    image TEXT NOT NULL,
    command TEXT NOT NULL,
    args TEXT NOT NULL DEFAULT '[]',
    gpu_type TEXT NOT NULL DEFAULT '',
    gpus BIGINT NOT NULL CHECK (gpus >= 0),
    cpus BIGINT NOT NULL CHECK (cpus > 0),
    memory_gb BIGINT NOT NULL CHECK (memory_gb > 0),
    duration_seconds BIGINT NOT NULL CHECK (duration_seconds >= 0),
    max_attempts BIGINT NOT NULL CHECK (max_attempts > 0),
    retry_backoff_seconds BIGINT NOT NULL CHECK (retry_backoff_seconds >= 0),
    created_at BIGINT NOT NULL
);

CREATE INDEX function_versions_function_id_idx ON function_versions (function_id, created_at DESC);

ALTER TABLE jobs ADD COLUMN function_version_id TEXT NOT NULL REFERENCES function_versions(id);
