CREATE INDEX CONCURRENTLY meeting_session_project ON meeting_session (workspace_id, project_id, created_at DESC) WHERE project_id IS NOT NULL;
