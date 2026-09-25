-- 005_log_stream.sql
Create table job_logs (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    attempt INTEGER NOT NULL,
    sequence INTEGER NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('stdout', 'stderr')),
    data TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
