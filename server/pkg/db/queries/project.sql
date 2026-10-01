-- name: ListProjects :many
SELECT * FROM project
WHERE workspace_id = $1
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
  AND (sqlc.narg('priority')::text IS NULL OR priority = sqlc.narg('priority'))
ORDER BY created_at DESC;

-- name: GetProjectInWorkspace :one
SELECT * FROM project
WHERE id = $1 AND workspace_id = $2;

-- name: LockProjectForChatSessionCreate :one
-- Conflicts with project deletion so a chat session cannot commit a soft
-- project reference after the delete transaction has swept existing sessions.
SELECT id FROM project
WHERE id = $1 AND workspace_id = $2
FOR KEY SHARE;

-- name: LockProjectForDelete :one
-- Serializes project deletion with chat-session creation. The handler locks,
-- clears every soft chat reference, and deletes the project in one transaction.
SELECT id FROM project
WHERE id = $1 AND workspace_id = $2
FOR UPDATE;

-- name: CreateProject :one
INSERT INTO project (
    workspace_id, title, description, icon, status,
    lead_type, lead_id, priority, start_date, due_date
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
) RETURNING *;

-- name: UpdateProject :one
UPDATE project SET
    title = COALESCE(sqlc.narg('title'), title),
    description = sqlc.narg('description'),
    icon = sqlc.narg('icon'),
    status = COALESCE(sqlc.narg('status'), status),
    priority = COALESCE(sqlc.narg('priority'), priority),
    lead_type = sqlc.narg('lead_type'),
    lead_id = sqlc.narg('lead_id'),
    start_date = sqlc.narg('start_date'),
    due_date = sqlc.narg('due_date'),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteProject :exec
-- Defense-in-depth: workspace_id is a SQL-layer tenant guard. See DeleteIssue.
DELETE FROM project WHERE id = $1 AND workspace_id = $2;

-- name: CountIssuesByProject :one
SELECT count(*) FROM issue
WHERE project_id = $1;

-- name: GetProjectIssueStats :many
SELECT project_id,
       count(*)::bigint AS total_count,
       count(*) FILTER (WHERE status = ANY(sqlc.arg('terminal_status_keys')::text[]))::bigint AS done_count
FROM issue
WHERE workspace_id = sqlc.arg('workspace_id')::uuid
  AND project_id = ANY(sqlc.arg('project_ids')::uuid[])
GROUP BY project_id;

-- name: CreateProjectGraphEvent :one
INSERT INTO project_graph_event (
    workspace_id, project_id, task_id, event_type, node_id, data
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: GetAgentTaskProjectID :one
-- Resolve the task's current source project using the claim source precedence.
-- Issue, chat, and run-only autopilot tasks carry the reference in different
-- source rows; quick-create keeps it in its JSON context. Invalid context
-- values are ignored rather than turning a task lookup into a cast failure.
SELECT CASE
    WHEN task.issue_id IS NOT NULL THEN issue.project_id
    WHEN task.chat_session_id IS NOT NULL THEN chat_session.project_id
    WHEN task.autopilot_run_id IS NOT NULL THEN autopilot.project_id
    WHEN task.context->>'type' = 'quick_create' THEN CASE
        WHEN task.context->>'project_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN (task.context->>'project_id')::uuid
        ELSE NULL
    END
    ELSE NULL
END::uuid AS project_id
FROM agent_task_queue AS task
LEFT JOIN issue ON issue.id = task.issue_id
LEFT JOIN chat_session ON chat_session.id = task.chat_session_id
LEFT JOIN autopilot_run ON autopilot_run.id = task.autopilot_run_id
LEFT JOIN autopilot ON autopilot.id = autopilot_run.autopilot_id
WHERE task.id = $1;

-- name: DeleteProjectGraphEvents :exec
DELETE FROM project_graph_event WHERE project_id=$1 AND workspace_id=$2;
