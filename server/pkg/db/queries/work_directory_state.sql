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

-- name: UpdateRuntimeWorkDirectoryStates :execrows
-- Filesystem availability is independent of the task lifecycle.
WITH reported AS MATERIALIZED (
  SELECT unnest(sqlc.arg(paths)::text[]) AS path,
         unnest(sqlc.arg(missing_states)::boolean[]) AS missing
)
UPDATE agent_task_queue task
SET work_dir_missing = reported.missing
FROM agent, reported
WHERE agent.id = task.agent_id
  AND agent.workspace_id = sqlc.arg(workspace_id)
  AND task.runtime_id = sqlc.arg(runtime_id)
  AND COALESCE(NULLIF(task.work_dir, ''), NULLIF(task.durable_work_dir, '')) = reported.path
  AND task.work_dir_missing IS DISTINCT FROM reported.missing;

-- name: GetChatFileParentTask :one
SELECT * FROM agent_task_queue WHERE id = $1 AND NOT work_dir_missing;
