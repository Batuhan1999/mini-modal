CREATE TABLE jobs (
    id TEXT PRIMARY KEY,
    priority BIGINT NOT NULL,
    gpu_type TEXT NOT NULL DEFAULT '',
    gpus BIGINT NOT NULL CHECK (gpus >= 0),
    cpus BIGINT NOT NULL CHECK (cpus >= 0),
    memory_gb BIGINT NOT NULL CHECK (memory_gb >= 0),
    duration_seconds BIGINT NOT NULL CHECK (duration_seconds >= 0),
    status TEXT NOT NULL CHECK (status IN ('pending', 'assigned', 'running', 'retrying', 'succeeded', 'failed', 'unschedulable', 'cancelling', 'cancelled')),
    node_id TEXT NOT NULL DEFAULT '',
    attempt BIGINT NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    lease_expires_at BIGINT NOT NULL DEFAULT 0,
    command TEXT NOT NULL DEFAULT '',
    args TEXT NOT NULL DEFAULT '[]',
    updated_at BIGINT NOT NULL DEFAULT 0,
    image TEXT NOT NULL,
    max_attempts BIGINT NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
    retry_backoff_seconds BIGINT NOT NULL DEFAULT 1 CHECK (retry_backoff_seconds >= 0),
    next_attempt_at BIGINT NOT NULL DEFAULT 0 CHECK (next_attempt_at >= 0),
    last_error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX jobs_status_idx ON jobs (status);
CREATE INDEX jobs_updated_at_idx ON jobs (updated_at DESC, id);
CREATE INDEX jobs_retry_eligibility_idx ON jobs (status, next_attempt_at, priority DESC, id);

CREATE TABLE node_pools (
    id TEXT PRIMARY KEY,
    gpu_type TEXT NOT NULL DEFAULT '',
    gpus_per_node BIGINT NOT NULL DEFAULT 0 CHECK (gpus_per_node >= 0),
    cpus_per_node BIGINT NOT NULL CHECK (cpus_per_node > 0),
    memory_gb_per_node BIGINT NOT NULL CHECK (memory_gb_per_node > 0),
    min_nodes BIGINT NOT NULL DEFAULT 0 CHECK (min_nodes >= 0),
    max_nodes BIGINT NOT NULL CHECK (max_nodes > 0),
    desired_nodes BIGINT NOT NULL DEFAULT 0 CHECK (desired_nodes >= 0),
    idle_timeout_seconds BIGINT NOT NULL DEFAULT 0 CHECK (idle_timeout_seconds >= 0),
    autoscale_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    CHECK (min_nodes <= max_nodes),
    CHECK (desired_nodes BETWEEN min_nodes AND max_nodes)
);

CREATE TABLE nodes (
    id TEXT PRIMARY KEY,
    pool_id TEXT NOT NULL REFERENCES node_pools(id),
    gpu_type TEXT NOT NULL DEFAULT '',
    total_gpus BIGINT NOT NULL CHECK (total_gpus >= 0),
    total_cpus BIGINT NOT NULL CHECK (total_cpus > 0),
    total_memory_gb BIGINT NOT NULL CHECK (total_memory_gb > 0)
);

CREATE INDEX nodes_pool_id_idx ON nodes (pool_id);

CREATE TABLE job_logs (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    attempt BIGINT NOT NULL,
    sequence BIGINT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('stdout', 'stderr', 'system')),
    data TEXT NOT NULL,
    created_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX job_logs_job_attempt_sequence_idx ON job_logs (job_id, attempt, sequence);

CREATE TABLE job_events (
    id TEXT PRIMARY KEY,
    event_seq BIGINT GENERATED ALWAYS AS IDENTITY,
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    attempt BIGINT NOT NULL,
    event TEXT NOT NULL CHECK (event IN ('submitted', 'assigned', 'claimed', 'completed', 'attempt_failed', 'failed', 'retry_scheduled', 'retry_ready', 'cancellation_requested', 'cancelled', 'lease_expired')),
    node_id TEXT NOT NULL,
    message TEXT NOT NULL,
    created_at BIGINT NOT NULL
);
CREATE INDEX job_events_job_attempt_idx ON job_events (job_id, attempt, created_at, event_seq);
