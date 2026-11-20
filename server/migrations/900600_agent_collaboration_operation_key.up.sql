CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS agent_collaboration_operation_key
    ON agent_collaboration_operation (workspace_id, actor_type, actor_id, client_request_id);
