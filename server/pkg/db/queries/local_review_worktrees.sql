-- name: ListLocalReviewWorktrees :many
SELECT task.id AS task_id, task.runtime_id, task.agent_id, task.issue_id, task.work_dir, task.status, task.branch_name, agent.workspace_id
FROM agent_task_queue task
JOIN agent ON agent.id = task.agent_id
JOIN agent_runtime runtime ON runtime.id = task.runtime_id AND runtime.workspace_id = agent.workspace_id
WHERE agent.workspace_id = sqlc.arg(workspace_id)
AND task.work_dir IS NOT NULL AND task.work_dir <> ''
AND (runtime.owner_id = sqlc.arg(reader_id) OR runtime.visibility = 'public')
AND (task.issue_id IS NOT NULL OR runtime.owner_id = sqlc.arg(reader_id))
AND (sqlc.narg(agent_id)::uuid IS NULL OR task.agent_id = sqlc.narg(agent_id))
ORDER BY task.created_at DESC, task.id DESC LIMIT 100 OFFSET sqlc.arg(page_offset);
