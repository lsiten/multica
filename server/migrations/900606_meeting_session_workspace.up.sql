CREATE INDEX CONCURRENTLY meeting_session_workspace_created ON meeting_session (workspace_id, created_at DESC, id DESC);
