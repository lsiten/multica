-- name: ListProjectCollaborationRuns :many
-- Resolve recorded run sources and structural parent issues before filtering.
WITH RECURSIVE base AS (
 SELECT t.id,t.agent_id,a.name AS agent_name,t.issue_id,t.squad_id,t.trigger_comment_id,
        t.status,t.created_at,t.started_at,t.completed_at,t.delegated_from_task_id,
        t.retry_of_task_id,t.rerun_of_task_id,t.parent_task_id,i.parent_issue_id,
        COALESCE(i.title,cs.title,'')::text AS issue_title,
        CASE WHEN i.id IS NULL THEN '' ELSE w.issue_prefix || '-' || i.number::text END::text AS issue_key,
        COALESCE(i.status,'')::text AS issue_status,
        COALESCE(i.status NOT IN ('done','cancelled') AND COALESCE(ist.category,'') NOT IN ('done','closed'),false)::boolean AS task_active,
        CASE
         WHEN t.issue_id IS NOT NULL THEN i.project_id
         WHEN t.chat_session_id IS NOT NULL THEN cs.project_id
         WHEN t.autopilot_run_id IS NOT NULL THEN ap.project_id
         WHEN t.context->>'type'='quick_create' AND t.context->>'project_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN (t.context->>'project_id')::uuid
        END::uuid AS project_id
 FROM agent_task_queue t
 JOIN agent a ON a.id=t.agent_id AND a.workspace_id = @workspace_id::uuid
 JOIN workspace w ON w.id=a.workspace_id
 LEFT JOIN issue i ON i.id=t.issue_id AND i.workspace_id=a.workspace_id
 LEFT JOIN issue_status ist ON ist.workspace_id=i.workspace_id AND ist.key=i.status
 LEFT JOIN chat_session cs ON cs.id=t.chat_session_id AND cs.workspace_id=a.workspace_id
 LEFT JOIN autopilot_run ar ON ar.id=t.autopilot_run_id
 LEFT JOIN autopilot ap ON ap.id=ar.autopilot_id AND ap.workspace_id=a.workspace_id
 WHERE (@is_admin::boolean OR a.owner_id = @user_id::uuid OR
  (a.permission_mode='public_to' AND EXISTS(SELECT 1 FROM agent_invocation_target ait WHERE ait.agent_id=a.id
   AND (ait.target_type='workspace' OR (ait.target_type='member' AND ait.target_id = @user_id::uuid)))))
 AND (t.chat_session_id IS NULL OR (cs.creator_id = @user_id::uuid AND (cs.explicitly_created_at IS NOT NULL OR
  EXISTS(SELECT 1 FROM chat_message cm WHERE cm.chat_session_id=cs.id AND cm.message_kind!='channel_command'))))
), visible AS MATERIALIZED (
 SELECT * FROM base WHERE project_id = @project_id::uuid
), resolved AS (
 SELECT v.*,source.id AS source_task_id,
        CASE WHEN source.id IS NOT NULL THEN source.agent_id ELSE pa.id END::uuid AS source_agent_id,
        COALESCE(source.agent_name,pa.name,'')::text AS source_agent_name,
        CASE WHEN source.id IS NOT NULL THEN source.issue_id WHEN pa.id IS NOT NULL OR parent.assignee_type IS DISTINCT FROM 'agent' THEN parent.id ELSE NULL END::uuid AS source_issue_id,
        CASE WHEN v.retry_of_task_id IS NOT NULL THEN 'retry'
             WHEN v.rerun_of_task_id IS NOT NULL THEN 'rerun'
             WHEN v.delegated_from_task_id IS NOT NULL THEN 'delegated'
             WHEN v.parent_task_id IS NOT NULL OR v.parent_issue_id IS NOT NULL THEN 'parent_child'
             ELSE 'root' END::text AS relation_type
 FROM visible v
 LEFT JOIN visible source ON source.id=COALESCE(v.retry_of_task_id,v.rerun_of_task_id,v.delegated_from_task_id,v.parent_task_id)
                         AND source.project_id=v.project_id
 LEFT JOIN issue parent ON parent.id=v.parent_issue_id AND parent.workspace_id = @workspace_id::uuid AND parent.project_id=v.project_id
     AND v.retry_of_task_id IS NULL AND v.rerun_of_task_id IS NULL AND v.delegated_from_task_id IS NULL AND v.parent_task_id IS NULL
 LEFT JOIN agent pa ON parent.assignee_type='agent' AND pa.id=parent.assignee_id AND pa.workspace_id=parent.workspace_id
     AND (@is_admin::boolean OR pa.owner_id = @user_id::uuid OR
       (pa.permission_mode='public_to' AND EXISTS(SELECT 1 FROM agent_invocation_target ait WHERE ait.agent_id=pa.id
        AND (ait.target_type='workspace' OR (ait.target_type='member' AND ait.target_id = @user_id::uuid)))))
), searchable_parents AS MATERIALIZED (
 SELECT p.* FROM issue p LEFT JOIN agent a ON p.assignee_type='agent' AND a.id=p.assignee_id AND a.workspace_id=p.workspace_id
 WHERE p.workspace_id = @workspace_id::uuid AND p.project_id = @project_id::uuid AND sqlc.arg('task_query')::text<>''
  AND (@is_admin::boolean OR COALESCE(p.assignee_type,'') <> 'agent' OR a.owner_id = @user_id::uuid OR
   (a.permission_mode='public_to' AND EXISTS(SELECT 1 FROM agent_invocation_target ait WHERE ait.agent_id=a.id AND
    (ait.target_type='workspace' OR (ait.target_type='member' AND ait.target_id = @user_id::uuid)))))
), issue_ancestors AS (
 SELECT child.id AS root_issue_id,p.id,p.parent_issue_id,p.title,p.number,p.assignee_type,p.assignee_id,ARRAY[p.id] AS path
 FROM issue child JOIN searchable_parents p ON p.id=child.parent_issue_id AND p.workspace_id=child.workspace_id AND p.project_id=child.project_id
 WHERE child.workspace_id = @workspace_id::uuid AND child.project_id = @project_id::uuid AND sqlc.arg('task_query')::text<>''
 UNION ALL
 SELECT child.root_issue_id,p.id,p.parent_issue_id,p.title,p.number,p.assignee_type,p.assignee_id,child.path || p.id
 FROM issue_ancestors child JOIN searchable_parents p ON p.id=child.parent_issue_id
 WHERE p.workspace_id = @workspace_id::uuid AND p.project_id = @project_id::uuid AND NOT p.id=ANY(child.path) AND cardinality(child.path)<32
), selected AS (
 SELECT * FROM resolved v
 WHERE v.project_id = @project_id::uuid
  AND (sqlc.narg('from_at')::timestamptz IS NULL OR v.created_at>=sqlc.narg('from_at'))
  AND (sqlc.narg('to_at')::timestamptz IS NULL OR v.created_at<=sqlc.narg('to_at'))
  AND (sqlc.narg('issue_id')::uuid IS NULL OR v.issue_id=sqlc.narg('issue_id'))
  AND (sqlc.narg('squad_id')::uuid IS NULL OR v.squad_id=sqlc.narg('squad_id'))
  AND (sqlc.narg('agent_id')::uuid IS NULL OR v.agent_id=sqlc.narg('agent_id') OR v.source_agent_id=sqlc.narg('agent_id'))
  AND (sqlc.narg('node_agent_id')::uuid IS NULL OR v.agent_id=sqlc.narg('node_agent_id') OR v.source_agent_id=sqlc.narg('node_agent_id'))
  AND (sqlc.narg('run_id')::uuid IS NULL OR v.id=sqlc.narg('run_id'))
  AND (sqlc.arg('activity_filter')::text IN ('','all') OR
       (sqlc.arg('activity_filter')::text='active' AND v.issue_id IS NOT NULL AND v.task_active) OR
       (sqlc.arg('activity_filter')::text='ended' AND v.issue_id IS NOT NULL AND NOT v.task_active))
  AND (sqlc.arg('relation_type')::text='' OR v.relation_type=sqlc.arg('relation_type')::text)
  AND (@status_filter::text='' OR v.status = @status_filter::text)
  AND (sqlc.narg('cursor_at')::timestamptz IS NULL OR
       (@oldest_first::boolean AND (v.created_at,v.id) > (sqlc.narg('cursor_at')::timestamptz,sqlc.narg('cursor_id')::uuid)) OR
       (NOT @oldest_first::boolean AND (v.created_at,v.id) < (sqlc.narg('cursor_at')::timestamptz,sqlc.narg('cursor_id')::uuid)))
  AND (sqlc.arg('task_query')::text='' OR v.issue_title ILIKE '%' || sqlc.arg('task_query')::text || '%' OR v.issue_key ILIKE '%' || sqlc.arg('task_query')::text || '%'
       OR EXISTS (
        SELECT 1 FROM issue_ancestors p LEFT JOIN agent a ON p.assignee_type='agent' AND a.id=p.assignee_id AND a.workspace_id = @workspace_id::uuid
        JOIN workspace w ON w.id = @workspace_id::uuid
        WHERE p.root_issue_id=v.issue_id AND (p.title ILIKE '%' || sqlc.arg('task_query')::text || '%' OR (w.issue_prefix || '-' || p.number::text) ILIKE '%' || sqlc.arg('task_query')::text || '%')
         AND (@is_admin::boolean OR COALESCE(p.assignee_type,'') <> 'agent' OR a.owner_id = @user_id::uuid OR
          (a.permission_mode='public_to' AND EXISTS(SELECT 1 FROM agent_invocation_target ait WHERE ait.agent_id=a.id AND
           (ait.target_type='workspace' OR (ait.target_type='member' AND ait.target_id = @user_id::uuid)))))
       ))
 ORDER BY CASE WHEN @oldest_first::boolean THEN v.created_at END ASC,
          CASE WHEN @oldest_first::boolean THEN v.id END ASC,
          v.created_at DESC,v.id DESC LIMIT @scan_limit::int
)
SELECT s.*,(SELECT count(*) FROM project_graph_event e WHERE e.task_id=s.id AND e.project_id = @project_id::uuid AND e.workspace_id = @workspace_id::uuid)::bigint AS event_count
FROM selected s ORDER BY CASE WHEN @oldest_first::boolean THEN s.created_at END ASC,
                        CASE WHEN @oldest_first::boolean THEN s.id END ASC,
                        s.created_at DESC,s.id DESC;

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

-- name: ListCollaborationIssueHierarchy :many
WITH RECURSIVE eligible AS MATERIALIZED (
    SELECT i.id, i.parent_issue_id, i.title, i.status, (w.issue_prefix || '-' || i.number::text)::text AS issue_key
    FROM issue i JOIN workspace w ON w.id=i.workspace_id
    LEFT JOIN agent a ON i.assignee_type='agent' AND a.id=i.assignee_id AND a.workspace_id=i.workspace_id
    WHERE i.workspace_id= @workspace_id::uuid AND i.project_id= @project_id::uuid
      AND (@is_admin::boolean OR i.id=ANY(@issue_ids::uuid[]) OR COALESCE(i.assignee_type,'') <> 'agent' OR a.owner_id= @user_id::uuid OR
        (a.permission_mode='public_to' AND EXISTS (SELECT 1 FROM agent_invocation_target ait WHERE ait.agent_id=a.id
          AND (ait.target_type='workspace' OR (ait.target_type='member' AND ait.target_id= @user_id::uuid)))))
), lineage AS (
    SELECT e.*, ARRAY[e.id] AS path FROM eligible e WHERE e.id=ANY(@issue_ids::uuid[])
    UNION ALL
    SELECT p.*, l.path || p.id FROM lineage l JOIN eligible p ON p.id=l.parent_issue_id
    WHERE NOT p.id=ANY(l.path) AND cardinality(l.path)<32
)
SELECT DISTINCT l.id, l.title, l.status, l.issue_key,
       CASE WHEN EXISTS(SELECT 1 FROM eligible p WHERE p.id=l.parent_issue_id) THEN l.parent_issue_id ELSE NULL END::uuid AS parent_issue_id
FROM lineage l ORDER BY l.id;

-- name: ListCollaborationRunArtifacts :many
SELECT a.id, a.task_id, a.filename, a.created_at
FROM attachment a
WHERE a.workspace_id= @workspace_id::uuid AND a.task_id=ANY(@task_ids::uuid[])
ORDER BY a.created_at, a.id;

-- name: ListCollaborationRunEvents :many
SELECT e.id,e.task_id,e.event_type,e.created_at
FROM project_graph_event e
WHERE e.workspace_id = @workspace_id::uuid AND e.project_id = @project_id::uuid AND e.task_id=ANY(@task_ids::uuid[])
ORDER BY e.created_at,e.id;
