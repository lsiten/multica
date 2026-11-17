-- name: ObserveWorkspaceFileResources :many
INSERT INTO workspace_file_resource (workspace_id, directory_key, path_key, relative_path, directory, binding_ref)
SELECT sqlc.arg('workspace_id')::uuid, sqlc.arg('directory_key')::text,
       entry.path_key, entry.relative_path, entry.directory, sqlc.arg('binding_ref')::jsonb
FROM jsonb_to_recordset(sqlc.arg('entries')::jsonb)
    AS entry(path_key text, relative_path text, directory boolean)
ON CONFLICT (workspace_id, directory_key, path_key) WHERE state = 'active'
DO UPDATE SET directory = CASE WHEN sqlc.arg('observed')::boolean THEN EXCLUDED.directory ELSE workspace_file_resource.directory END,
              binding_ref = EXCLUDED.binding_ref,
              updated_at = now()
RETURNING *;

-- name: GetWorkspaceFileResource :one
SELECT * FROM workspace_file_resource
WHERE workspace_id = $1 AND directory_key = $2 AND id = $3;

-- name: DeleteWorkspaceFileResourcesForWorkspace :exec
DELETE FROM workspace_file_resource WHERE workspace_id = $1;

-- name: DeleteWorkspaceFileResourcesForOwner :exec
DELETE FROM workspace_file_resource
WHERE workspace_id = $1 AND binding_ref->>'ownerKind' = sqlc.arg('owner_kind')::text
  AND binding_ref->>'ownerId' = sqlc.arg('owner_id')::uuid::text;

-- name: DeleteWorkspaceFileResourcesForProject :exec
DELETE FROM workspace_file_resource AS file
WHERE file.workspace_id = sqlc.arg('workspace_id')
  AND file.binding_ref->>'ownerKind' = 'project_resource'
  AND file.binding_ref->>'ownerId' IN (
    SELECT binding.id::text FROM project_resource binding WHERE binding.workspace_id = sqlc.arg('workspace_id') AND binding.project_id = sqlc.arg('project_id')
  );

-- name: AdoptLegacyWorkspaceFileComments :exec
UPDATE document_comment AS note
SET resource_key = 'workspace_file:' || sqlc.arg('file_id')::uuid::text,
    resource_ref = note.resource_ref || jsonb_build_object('fileId', sqlc.arg('file_id')::uuid::text,
        'ownerKind', sqlc.arg('owner_kind')::text, 'ownerId', sqlc.arg('owner_id')::text)
WHERE note.workspace_id = sqlc.arg('workspace_id')
  AND note.resource_key ~ '^workspace_file:[0-9a-f]{64}$'
  AND rtrim(replace(note.resource_ref->>'workDir', chr(92), '/'), '/') = rtrim(replace(sqlc.arg('work_dir')::text, chr(92), '/'), '/')
  AND note.resource_ref->>'relativePath' = sqlc.arg('relative_path')::text
  AND EXISTS (
    SELECT 1 FROM agent_runtime host
    WHERE host.workspace_id = note.workspace_id
      AND host.id::text = note.resource_ref->>'runtimeId'
      AND host.owner_id = sqlc.arg('runtime_owner_id')
      AND ((sqlc.arg('daemon_id')::text <> '' AND host.daemon_id = sqlc.arg('daemon_id'))
        OR (sqlc.arg('daemon_id')::text = '' AND host.id = sqlc.arg('runtime_id')))
  )
  AND (
    EXISTS (
      SELECT 1 FROM agent_task_queue task
      JOIN agent owner ON owner.id = task.agent_id AND owner.workspace_id = note.workspace_id
      WHERE task.id::text = note.resource_ref->>'taskId'
        AND (
          (sqlc.arg('owner_kind')::text = 'chat' AND task.chat_session_id::text = sqlc.arg('owner_id')::text)
          OR (sqlc.arg('owner_kind')::text = 'task' AND task.id::text = sqlc.arg('owner_id')::text)
          OR (sqlc.arg('owner_kind')::text = 'issue' AND task.chat_session_id IS NULL AND
            CASE WHEN task.result ? 'code_workspace'
                 THEN task.result->'code_workspace'->>'work_dir' = task.work_dir
                      AND task.result->'code_workspace'->>'runtime_id' = task.runtime_id::text
                      AND task.result->'code_workspace'->>'issue_id' = sqlc.arg('owner_id')::text
                 ELSE task.issue_id::text = sqlc.arg('owner_id')::text END)
        )
    ) OR EXISTS (
      SELECT 1 FROM project_resource binding
      WHERE binding.workspace_id = note.workspace_id
        AND binding.id::text = note.resource_ref->>'projectResourceId'
        AND sqlc.arg('owner_kind')::text = 'project_resource'
        AND binding.id::text = sqlc.arg('owner_id')::text
    )
  );
