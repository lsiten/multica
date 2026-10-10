CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS execution_snapshot_identity_idx ON execution_snapshot (runtime_id, snapshot_id);
