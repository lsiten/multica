-- name: EnsureWorkspaceFileHost :exec
INSERT INTO workspace_file_host_state (workspace_id, host_key) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: LockWorkspaceFileHost :one
SELECT * FROM workspace_file_host_state WHERE workspace_id = $1 AND host_key = $2 FOR UPDATE;

-- name: BumpWorkspaceFileHostRevision :one
UPDATE workspace_file_host_state SET revision = revision + 1 WHERE workspace_id = $1 AND host_key = $2 RETURNING revision;

-- name: WorkspaceFileOperationOverlaps :one
SELECT EXISTS (
    SELECT 1 FROM workspace_file_operation
    WHERE workspace_id = sqlc.arg('workspace_id') AND host_key = sqlc.arg('host_key') AND state = 'pending'
      AND (root_path = sqlc.arg('root_path')::text
        OR strpos(root_path, rtrim(sqlc.arg('root_path')::text, '/') || '/') = 1
        OR strpos(sqlc.arg('root_path')::text, rtrim(root_path, '/') || '/') = 1)
)::boolean;

-- name: GetWorkspaceFileOperationByCommand :one
SELECT * FROM workspace_file_operation WHERE workspace_id = $1 AND physical_key = $2 AND command_id = $3;

-- name: ListWorkspaceFileOperationsByActorCommand :many
SELECT * FROM workspace_file_operation
WHERE workspace_id = sqlc.arg('workspace_id') AND actor_id = sqlc.arg('actor_id') AND command_id = sqlc.arg('command_id')
ORDER BY created_at DESC
LIMIT 2;

-- name: GetWorkspaceFileOperation :one
SELECT * FROM workspace_file_operation WHERE workspace_id = $1 AND id = $2;

-- name: CreateWorkspaceFileOperation :one
INSERT INTO workspace_file_operation (workspace_id, host_key, physical_key, directory_key, root_path, command_id, actor_id, request_hash, operation, binding_ref, resources)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING *;

-- name: FinishWorkspaceFileOperation :execrows
UPDATE workspace_file_operation SET state = $3, result = $4, completed_at = now()
WHERE workspace_id = $1 AND id = $2 AND state = 'pending';

-- name: MarkWorkspaceFileOperationDispatched :one
UPDATE workspace_file_operation SET dispatch_started = true WHERE workspace_id = $1 AND id = $2 AND state = 'pending' RETURNING *;

-- name: ListWorkspaceFileHostResources :many
SELECT file.* FROM workspace_file_resource file
WHERE file.workspace_id = sqlc.arg('workspace_id') AND file.id > sqlc.arg('after_id')::uuid AND file.state = 'active'
  AND (EXISTS (
    SELECT 1 FROM agent_runtime runtime WHERE runtime.workspace_id = file.workspace_id
      AND runtime.id::text = file.binding_ref->>'runtimeId' AND runtime.owner_id = sqlc.arg('runtime_owner_id')
      AND ((sqlc.arg('daemon_id')::text <> '' AND runtime.daemon_id = sqlc.arg('daemon_id'))
        OR (sqlc.arg('daemon_id')::text = '' AND runtime.id = sqlc.arg('runtime_id')))
  ) OR NOT EXISTS (
    SELECT 1 FROM agent_runtime route WHERE route.workspace_id = file.workspace_id AND route.id::text = file.binding_ref->>'runtimeId'
  ))
ORDER BY file.id LIMIT 500;

-- name: GetWorkspaceFileResourceByID :one
SELECT * FROM workspace_file_resource WHERE workspace_id = $1 AND id = $2;

-- name: WorkspaceFilePathWasPublished :one
SELECT EXISTS (
    SELECT 1 FROM workspace_file_operation
    WHERE workspace_id = sqlc.arg('workspace_id') AND host_key = sqlc.arg('host_key') AND state = 'completed'
      AND operation->>'kind' IN ('create_file', 'create_directory', 'move', 'restore')
      AND (sqlc.arg('absolute_path')::text = rtrim(root_path, '/') || '/' || CASE WHEN operation->>'kind' = 'move' THEN operation->>'destination' ELSE operation->>'path' END
        OR strpos(sqlc.arg('absolute_path')::text, rtrim(root_path, '/') || '/' || CASE WHEN operation->>'kind' = 'move' THEN operation->>'destination' ELSE operation->>'path' END || '/') = 1)
)::boolean;

-- name: ListLegacyWorkspaceFileSourcesOnHost :many
SELECT DISTINCT note.resource_ref FROM document_comment note
WHERE note.workspace_id = sqlc.arg('workspace_id') AND note.resource_key ~ '^workspace_file:[0-9a-f]{64}$'
  AND EXISTS (
    SELECT 1 FROM agent_runtime runtime WHERE runtime.workspace_id = note.workspace_id
      AND runtime.id::text = note.resource_ref->>'runtimeId' AND runtime.owner_id = sqlc.arg('runtime_owner_id')
      AND ((sqlc.arg('daemon_id')::text <> '' AND runtime.daemon_id = sqlc.arg('daemon_id'))
        OR (sqlc.arg('daemon_id')::text = '' AND runtime.id = sqlc.arg('runtime_id')))
  );

-- name: RelocateWorkspaceFileResource :execrows
UPDATE workspace_file_resource
SET relative_path = sqlc.arg('new_path'), path_key = sqlc.arg('new_path_key'), state = sqlc.arg('new_state'),
    recovery_id = sqlc.narg('new_recovery_id'), directory = sqlc.arg('directory'),
    binding_ref = binding_ref || jsonb_build_object('relativePath', sqlc.arg('new_path')::text), updated_at = now()
WHERE workspace_id = sqlc.arg('workspace_id') AND id = sqlc.arg('id')
  AND state = sqlc.arg('previous_state') AND relative_path = sqlc.arg('previous_path')
  AND recovery_id IS NOT DISTINCT FROM sqlc.narg('previous_recovery_id')::text;

-- name: DeleteWorkspaceFileCoordinationForWorkspace :exec
WITH operations AS (DELETE FROM workspace_file_operation AS operation WHERE operation.workspace_id = $1)
DELETE FROM workspace_file_host_state AS host WHERE host.workspace_id = $1;
