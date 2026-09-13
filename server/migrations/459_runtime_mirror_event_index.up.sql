CREATE INDEX CONCURRENTLY runtime_mirror_event_workspace_time_idx
    ON runtime_mirror_event (workspace_id, created_at DESC);
