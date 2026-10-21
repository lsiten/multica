-- name: ListRuntimeWorkDirectories :many
-- The owning daemon reconciles persisted paths against its filesystem, including
-- previously missing paths so restoring an archive makes it browsable again.
SELECT DISTINCT (COALESCE(NULLIF(task.work_dir, ''), NULLIF(task.durable_work_dir, '')) COLLATE "C")::text AS path
FROM agent_task_queue task
JOIN agent ON agent.id = task.agent_id
WHERE task.runtime_id = sqlc.arg(runtime_id)
  AND agent.workspace_id = sqlc.arg(workspace_id)
  AND COALESCE(NULLIF(task.work_dir, ''), NULLIF(task.durable_work_dir, '')) COLLATE "C" > sqlc.arg(after_path)::text COLLATE "C"
ORDER BY path
LIMIT 500;

-- name: UpdateRuntimeWorkDirectoryState :execrows
UPDATE agent_task_queue task
SET work_dir_missing = sqlc.arg(missing)::boolean
FROM agent
WHERE agent.id = task.agent_id
  AND agent.workspace_id = sqlc.arg(workspace_id)
  AND task.runtime_id = sqlc.arg(runtime_id)
  AND (NOT sqlc.arg(missing)::boolean OR task.status IN ('completed', 'failed', 'cancelled'))
  AND COALESCE(NULLIF(task.work_dir, ''), NULLIF(task.durable_work_dir, '')) = sqlc.arg(path)::text
  AND task.work_dir_missing IS DISTINCT FROM sqlc.arg(missing)::boolean
  AND (NOT sqlc.arg(missing)::boolean OR NOT EXISTS (
    SELECT 1 FROM agent_task_queue active
    WHERE active.runtime_id = task.runtime_id
      AND active.status NOT IN ('completed', 'failed', 'cancelled')
      AND COALESCE(NULLIF(active.work_dir, ''), NULLIF(active.durable_work_dir, '')) = sqlc.arg(path)::text
  ));

-- name: GetChatFileParentTask :one
SELECT * FROM agent_task_queue WHERE id = $1 AND NOT work_dir_missing;
