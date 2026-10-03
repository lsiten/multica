-- name: GetLastAutomationWorkdir :one
-- Different automation runs share code only within the same owning workspace,
-- agent, runtime and automation. Provider sessions remain per-run.
SELECT q.work_dir
FROM agent_task_queue q
JOIN autopilot_run ar ON ar.id = q.autopilot_run_id
JOIN autopilot a ON a.id = ar.autopilot_id
WHERE a.workspace_id = sqlc.arg(workspace_id)
  AND a.id = sqlc.arg(autopilot_id)
  AND q.agent_id = sqlc.arg(agent_id)
  AND q.runtime_id = sqlc.arg(runtime_id)
  AND q.id <> sqlc.arg(current_task_id)
  AND q.issue_id IS NULL AND q.chat_session_id IS NULL
  AND q.status IN ('completed', 'failed', 'cancelled')
  AND q.work_dir IS NOT NULL AND q.work_dir <> ''
ORDER BY q.created_at DESC, q.id DESC
LIMIT 1;
