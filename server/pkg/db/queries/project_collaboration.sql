-- name: ListProjectCollaborationRuns :many
-- Resolve source ownership before filtering. Parent rows intentionally precede
-- child filters and pagination, so older parents remain provable evidence.
WITH visible AS MATERIALIZED (
    SELECT t.id, t.agent_id, a.name AS agent_name, t.issue_id, t.squad_id,
           t.trigger_comment_id, t.status, t.created_at, t.started_at, t.completed_at,
           t.delegated_from_task_id, t.retry_of_task_id, t.rerun_of_task_id,
           CASE
               WHEN t.issue_id IS NOT NULL THEN i.project_id
               WHEN t.chat_session_id IS NOT NULL THEN cs.project_id
               WHEN t.autopilot_run_id IS NOT NULL THEN ap.project_id
               WHEN t.context->>'type' = 'quick_create'
                    AND t.context->>'project_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
               THEN (t.context->>'project_id')::uuid
           END::uuid AS project_id
    FROM agent_task_queue t
    JOIN agent a ON a.id = t.agent_id AND a.workspace_id = @workspace_id::uuid
    LEFT JOIN issue i ON i.id = t.issue_id AND i.workspace_id = a.workspace_id
    LEFT JOIN chat_session cs ON cs.id = t.chat_session_id AND cs.workspace_id = a.workspace_id
    LEFT JOIN autopilot_run ar ON ar.id = t.autopilot_run_id
    LEFT JOIN autopilot ap ON ap.id = ar.autopilot_id AND ap.workspace_id = a.workspace_id
    WHERE (
        @is_admin::boolean OR a.owner_id = @user_id::uuid
        OR (a.permission_mode = 'public_to' AND EXISTS (
            SELECT 1 FROM agent_invocation_target ait WHERE ait.agent_id = a.id
              AND (ait.target_type = 'workspace' OR (ait.target_type = 'member' AND ait.target_id = @user_id::uuid))
        ))
    )
    AND (t.chat_session_id IS NULL OR (cs.creator_id = @user_id::uuid AND (
        cs.explicitly_created_at IS NOT NULL OR EXISTS (
            SELECT 1 FROM chat_message cm WHERE cm.chat_session_id = cs.id AND cm.message_kind != 'channel_command'
        )
    )))
), selected AS (
    SELECT * FROM visible v
    WHERE v.project_id = @project_id::uuid
      AND (sqlc.narg('from_at')::timestamptz IS NULL OR v.created_at >= sqlc.narg('from_at'))
      AND (sqlc.narg('to_at')::timestamptz IS NULL OR v.created_at <= sqlc.narg('to_at'))
      AND (sqlc.narg('issue_id')::uuid IS NULL OR v.issue_id = sqlc.narg('issue_id'))
      AND (sqlc.narg('squad_id')::uuid IS NULL OR v.squad_id = sqlc.narg('squad_id'))
      AND (sqlc.narg('agent_id')::uuid IS NULL OR v.agent_id = sqlc.narg('agent_id'))
      AND (@status_filter::text = '' OR v.status = @status_filter::text)
    ORDER BY v.created_at DESC, v.id DESC
    LIMIT @scan_limit::int
)
SELECT s.*, p.id AS source_task_id, p.agent_id AS source_agent_id,
       COALESCE(p.agent_name, '')::text AS source_agent_name,
       CASE WHEN s.retry_of_task_id IS NOT NULL THEN 'retry'
            WHEN s.rerun_of_task_id IS NOT NULL THEN 'rerun'
            WHEN s.delegated_from_task_id IS NOT NULL THEN 'delegated'
            ELSE 'root' END::text AS relation_type,
       (SELECT COUNT(DISTINCT e.event_type) FROM project_graph_event e
        WHERE e.task_id = s.id AND e.project_id = @project_id::uuid
          AND e.workspace_id = @workspace_id::uuid)::bigint AS event_count
FROM selected s
LEFT JOIN visible p ON p.id = COALESCE(s.retry_of_task_id, s.rerun_of_task_id, s.delegated_from_task_id)
                   AND p.project_id = s.project_id
ORDER BY s.created_at DESC, s.id DESC;

-- name: ListVisibleProjectGraphEvents :many
WITH visible AS MATERIALIZED (
    SELECT t.id, t.agent_id, a.name AS agent_name, t.issue_id, t.squad_id,
           t.trigger_comment_id, t.status, t.created_at, t.started_at, t.completed_at,
           t.delegated_from_task_id, t.retry_of_task_id, t.rerun_of_task_id,
           CASE
               WHEN t.issue_id IS NOT NULL THEN i.project_id
               WHEN t.chat_session_id IS NOT NULL THEN cs.project_id
               WHEN t.autopilot_run_id IS NOT NULL THEN ap.project_id
               WHEN t.context->>'type' = 'quick_create'
                    AND t.context->>'project_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
               THEN (t.context->>'project_id')::uuid
           END::uuid AS project_id
    FROM agent_task_queue t
    JOIN agent a ON a.id = t.agent_id AND a.workspace_id = @workspace_id::uuid
    LEFT JOIN issue i ON i.id = t.issue_id AND i.workspace_id = a.workspace_id
    LEFT JOIN chat_session cs ON cs.id = t.chat_session_id AND cs.workspace_id = a.workspace_id
    LEFT JOIN autopilot_run ar ON ar.id = t.autopilot_run_id
    LEFT JOIN autopilot ap ON ap.id = ar.autopilot_id AND ap.workspace_id = a.workspace_id
    WHERE (
        @is_admin::boolean OR a.owner_id = @user_id::uuid
        OR (a.permission_mode = 'public_to' AND EXISTS (
            SELECT 1 FROM agent_invocation_target ait WHERE ait.agent_id = a.id
              AND (ait.target_type = 'workspace' OR (ait.target_type = 'member' AND ait.target_id = @user_id::uuid))
        ))
    )
    AND (t.chat_session_id IS NULL OR (cs.creator_id = @user_id::uuid AND (
        cs.explicitly_created_at IS NOT NULL OR EXISTS (
            SELECT 1 FROM chat_message cm WHERE cm.chat_session_id = cs.id AND cm.message_kind != 'channel_command'
        )
    )))
)
SELECT e.id, e.project_id, e.workspace_id, e.task_id, e.event_type, e.node_id, e.created_at,
       COALESCE((SELECT jsonb_agg(a.id::text ORDER BY a.created_at, a.id)
                 FROM attachment a
                 WHERE a.workspace_id=e.workspace_id AND a.task_id=e.task_id), '[]'::jsonb) AS artifact_attachment_ids,
       ((e.data - 'delegated_from_task_id' - 'retry_of_task_id' - 'rerun_of_task_id') ||
       CASE WHEN p.id IS NULL THEN '{}'::jsonb
            WHEN v.retry_of_task_id IS NOT NULL THEN jsonb_build_object('retry_of_task_id', p.id)
            WHEN v.rerun_of_task_id IS NOT NULL THEN jsonb_build_object('rerun_of_task_id', p.id)
            ELSE jsonb_build_object('delegated_from_task_id', p.id) END)::jsonb AS data
FROM project_graph_event e
JOIN visible v ON v.id = e.task_id AND v.project_id = e.project_id
LEFT JOIN visible p ON p.id = COALESCE(v.retry_of_task_id, v.rerun_of_task_id, v.delegated_from_task_id)
                   AND p.project_id = v.project_id
WHERE e.workspace_id = @workspace_id::uuid AND e.project_id = @project_id::uuid
ORDER BY e.created_at ASC, e.id ASC
LIMIT @page_limit::int OFFSET @page_offset::int;
