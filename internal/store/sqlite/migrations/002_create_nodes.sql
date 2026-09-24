CREATE TABLE IF NOT EXISTS nodes (
    id TEXT PRIMARY KEY,
    gpu_type TEXT NOT NULL DEFAULT '',
    total_gpus INTEGER NOT NULL CHECK (total_gpus >= 0),
    total_cpus INTEGER NOT NULL CHECK (total_cpus > 0),
    total_memory_gb INTEGER NOT NULL CHECK (total_memory_gb > 0)
);
