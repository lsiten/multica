-- name: GetWorkspaceJevConfig :one
SELECT config, revision FROM workspace_jev_config WHERE workspace_id=$1;

-- name: CreateWorkspaceJevConfig :one
INSERT INTO workspace_jev_config (workspace_id, config, revision) VALUES ($1,$2,1)
ON CONFLICT (workspace_id) DO NOTHING RETURNING config,revision;

-- name: UpdateWorkspaceJevConfig :one
UPDATE workspace_jev_config SET config=$2,revision=revision+1,updated_at=now()
WHERE workspace_id=$1 AND revision=$3 RETURNING config,revision;

-- name: DeleteWorkspaceJevConfig :exec
DELETE FROM workspace_jev_config WHERE workspace_id=$1;

-- name: CaptureTaskJevConfig :one
UPDATE agent_task_queue AS t SET context=COALESCE(t.context,'{}'::jsonb) || jsonb_build_object('workspace_jev', COALESCE(t.context->'workspace_jev',sqlc.arg(config)::jsonb))
WHERE t.id=sqlc.arg(task_id) AND EXISTS (SELECT 1 FROM agent a WHERE a.id=t.agent_id AND a.workspace_id=sqlc.arg(workspace_id))
RETURNING t.context;
