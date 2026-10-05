CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS human_request_source_key ON human_request (workspace_id, source_task_id, request_key);
