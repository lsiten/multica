CREATE INDEX CONCURRENTLY IF NOT EXISTS jev_decision_log_workspace_idx ON jev_decision_log (workspace_id, started_at DESC, id DESC);
