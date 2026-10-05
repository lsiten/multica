-- name: ListProgressIssues :many
WITH RECURSIVE descendants AS (
    SELECT i.id FROM issue i
    WHERE i.workspace_id = @workspace_id
      AND (i.id = sqlc.narg('root_id')::uuid OR i.project_id = sqlc.narg('project_id')::uuid)
    UNION
    SELECT child.id FROM issue child JOIN descendants parent ON child.parent_issue_id = parent.id
    WHERE child.workspace_id = @workspace_id
), scope AS (
    SELECT i.id FROM issue i
    WHERE i.workspace_id = @workspace_id AND i.project_id = sqlc.narg('project_id')::uuid
    UNION SELECT id FROM descendants WHERE sqlc.narg('root_id')::uuid IS NOT NULL
), reachable AS (
    SELECT id FROM descendants
    UNION
    SELECT next_issue.id FROM reachable r
    JOIN issue current_issue ON current_issue.id = r.id AND current_issue.workspace_id = @workspace_id
    CROSS JOIN LATERAL (
        SELECT current_issue.parent_issue_id AS id
        UNION
        SELECT CASE WHEN d.type = 'blocked_by' THEN d.depends_on_issue_id ELSE d.issue_id END
        FROM issue_dependency d
        WHERE (d.issue_id = r.id AND d.type = 'blocked_by')
           OR (d.depends_on_issue_id = r.id AND d.type = 'blocks')
    ) linked
    JOIN issue next_issue ON next_issue.id = linked.id AND next_issue.workspace_id = @workspace_id
)
SELECT i.id, i.number, i.title, i.status, i.priority, i.revision,
    i.assignee_type, i.assignee_id, i.parent_issue_id, i.project_id, i.stage,
    i.created_at, i.updated_at, i.due_date,
    COALESCE(s.category, '')::text AS status_category,
    COALESCE(s.name, '')::text AS status_name,
    COALESCE(p.title, '')::text AS project_title,
    EXISTS(SELECT 1 FROM scope WHERE scope.id = i.id)::boolean AS in_scope,
    latest.id AS latest_run_id, COALESCE(latest.status, '')::text AS latest_run_status,
    COALESCE(latest.completed_at, latest.started_at, latest.created_at) AS latest_run_at,
    active.id AS active_run_id, COALESCE(active.status, '')::text AS active_run_status,
    active.created_at AS active_run_at,
    COALESCE(a.archived_at IS NOT NULL OR (i.assignee_type = 'squad' AND sq.archived_at IS NOT NULL), false)::boolean AS assignee_archived,
    COALESCE(i.assignee_type IN ('agent','squad') AND i.assignee_id IS NOT NULL AND a.id IS NULL, false)::boolean AS assignee_missing,
    COALESCE(a.id IS NOT NULL AND (a.runtime_id IS NULL OR runtime.id IS NULL), false)::boolean AS runtime_missing,
    COALESCE(runtime.status, '')::text AS runtime_status,
    COALESCE((SELECT max(activity.created_at) FROM activity_log activity
        WHERE activity.workspace_id = i.workspace_id AND activity.issue_id = i.id AND activity.action = 'status_changed'), i.created_at)::timestamptz AS status_changed_at
FROM reachable JOIN issue i ON i.id = reachable.id AND i.workspace_id = @workspace_id
LEFT JOIN issue_status s ON s.workspace_id = i.workspace_id AND s.key = i.status
LEFT JOIN project p ON p.id = i.project_id AND p.workspace_id = i.workspace_id
LEFT JOIN squad sq ON i.assignee_type = 'squad' AND sq.id = i.assignee_id AND sq.workspace_id = i.workspace_id
LEFT JOIN agent a ON a.id = CASE WHEN i.assignee_type = 'agent' THEN i.assignee_id ELSE sq.leader_id END AND a.workspace_id = i.workspace_id
LEFT JOIN agent_runtime runtime ON runtime.id = a.runtime_id AND runtime.workspace_id = i.workspace_id
LEFT JOIN LATERAL (SELECT t.id,t.status,t.created_at,t.started_at,t.completed_at FROM agent_task_queue t
    WHERE t.issue_id = i.id ORDER BY t.created_at DESC,t.id DESC LIMIT 1) latest ON true
LEFT JOIN LATERAL (SELECT t.id,t.status,t.created_at FROM agent_task_queue t WHERE t.issue_id = i.id
    AND t.status IN ('queued','deferred','dispatched','running','waiting_local_directory')
    ORDER BY t.created_at DESC,t.id DESC LIMIT 1) active ON true
ORDER BY i.id LIMIT 10001;

-- name: ListProgressDependencies :many
SELECT d.id, d.issue_id, d.depends_on_issue_id, d.type
FROM issue_dependency d
WHERE (d.issue_id = ANY(@issue_ids::uuid[]) OR d.depends_on_issue_id = ANY(@issue_ids::uuid[]))
AND EXISTS(SELECT 1 FROM issue own WHERE own.workspace_id = @workspace_id
  AND (own.id = d.issue_id OR own.id = d.depends_on_issue_id))
ORDER BY d.id LIMIT 20001;

-- name: ListProgressHumanRequests :many
SELECT hr.* FROM human_request hr
WHERE hr.workspace_id = @workspace_id AND hr.status = 'pending' AND hr.expires_at > now()
AND (hr.issue_id = ANY(@issue_ids::uuid[])
    OR (hr.project_id = sqlc.narg('project_id')::uuid AND hr.recipient_id = @member_id))
ORDER BY hr.id LIMIT 1001;
