CREATE TABLE IF NOT EXISTS jobs (
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
            'unschedulable'
        )
    ),
    node_id TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0)
);

CREATE INDEX IF NOT EXISTS jobs_status_idx ON jobs (status);
