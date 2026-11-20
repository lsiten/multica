CREATE INDEX CONCURRENTLY agent_collaboration_operation_target
    ON agent_collaboration_operation (workspace_id, target_task_id, created_at DESC);
