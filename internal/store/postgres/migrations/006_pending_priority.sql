CREATE INDEX jobs_pending_priority_idx ON jobs (priority DESC, id) WHERE status = 'pending';
