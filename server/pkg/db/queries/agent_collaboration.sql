-- name: CreateAgentCollaborationOperation :one
INSERT INTO agent_collaboration_operation (
    id, workspace_id, actor_type, actor_id, client_request_id, operation,
    fingerprint, status, source_task_id, issue_id, target_agent_id, target_task_id,
    comment_id, result, error
)
VALUES (
    @id, @workspace_id, @actor_type, @actor_id, @client_request_id, @operation,
    @fingerprint, @status, @source_task_id, @issue_id, @target_agent_id, @target_task_id,
    @comment_id, COALESCE(@result::jsonb, '{}'::jsonb), @error
)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: GetAgentCollaborationOperationByKey :one
SELECT * FROM agent_collaboration_operation
WHERE workspace_id = @workspace_id
  AND actor_type = @actor_type
  AND actor_id = @actor_id
  AND client_request_id = @client_request_id;

-- name: CompleteAgentCollaborationOperation :one
UPDATE agent_collaboration_operation
SET status = @status,
    result = COALESCE(@result::jsonb, result),
    error = @error,
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: GetAgentCollaborationOperation :one
SELECT * FROM agent_collaboration_operation WHERE id = @id;

-- name: SetAgentCollaborationOperationTargets :one
UPDATE agent_collaboration_operation
SET issue_id = COALESCE(@issue_id, issue_id),
    target_agent_id = COALESCE(@target_agent_id, target_agent_id),
    target_task_id = COALESCE(@target_task_id, target_task_id),
    comment_id = COALESCE(@comment_id, comment_id),
    updated_at = now()
WHERE id = @id
RETURNING *;
