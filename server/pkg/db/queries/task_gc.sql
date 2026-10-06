-- name: ListTaskGCLifecycles :many
-- Resolve requested runs and their current resumable workline in one snapshot.
-- A completed run is not itself a reason to keep every checkout it ever used.
SELECT q.id, q.runtime_id, q.agent_id, q.issue_id, q.chat_session_id,
       q.autopilot_run_id, ar.autopilot_id, q.status, q.completed_at,
       q.work_dir, q.prepare_lease_expires_at, a.workspace_id,
       i.status AS issue_status, i.revision AS issue_revision,
       chat.status AS chat_status,
       COALESCE(i.last_activity_at, i.updated_at) AS last_activity_at,
       COALESCE(current_work.work_dir, '')::text AS current_work_dir,
       COALESCE(current_work.id::text, '')::text AS current_task_id,
       EXISTS (
           SELECT 1 FROM human_request request
           JOIN agent_task_queue source ON source.id = request.source_task_id
           WHERE request.workspace_id = a.workspace_id
             AND request.agent_id = q.agent_id
             AND source.runtime_id = q.runtime_id
			 AND source.agent_id = q.agent_id
             AND request.issue_id IS NOT DISTINCT FROM q.issue_id
             AND request.chat_session_id IS NOT DISTINCT FROM q.chat_session_id
             AND (q.issue_id IS NOT NULL OR q.chat_session_id IS NOT NULL OR request.source_task_id = q.id)
             AND request.status = 'pending' AND request.expires_at > now()
             AND source.status NOT IN ('failed', 'cancelled')
			 AND (q.issue_id IS NULL OR i.id IS NOT NULL)
			 AND (q.chat_session_id IS NULL OR EXISTS (
				 SELECT 1 FROM chat_session chat WHERE chat.id = q.chat_session_id
				 AND chat.workspace_id = a.workspace_id AND chat.status = 'active'
			 ))
       )::boolean AS waiting_human,
       COALESCE((a.archived_at IS NULL AND (
           q.issue_id IS NULL
           OR (a.runtime_id = q.runtime_id AND i.assignee_type = 'agent' AND i.assignee_id = q.agent_id)
           OR (issue_work.agent_id = q.agent_id AND issue_work.runtime_id = q.runtime_id)
       )), false)::boolean AS current_agent
FROM agent_task_queue q
JOIN agent a ON a.id = q.agent_id
LEFT JOIN issue i ON i.id = q.issue_id AND i.workspace_id = a.workspace_id
LEFT JOIN chat_session chat ON chat.id = q.chat_session_id AND chat.workspace_id = a.workspace_id
LEFT JOIN autopilot_run ar ON ar.id = q.autopilot_run_id
LEFT JOIN LATERAL (
    SELECT previous.agent_id, previous.runtime_id
    FROM agent_task_queue previous
    JOIN agent previous_agent ON previous_agent.id = previous.agent_id AND previous_agent.workspace_id = a.workspace_id
    WHERE previous.issue_id = q.issue_id AND previous.work_dir IS NOT NULL AND previous.work_dir <> ''
      AND COALESCE(previous.durable_work_dir, '') = ''
    ORDER BY previous.created_at DESC, previous.id DESC
    LIMIT 1
) issue_work ON true
LEFT JOIN LATERAL (
    SELECT previous.id, previous.work_dir
    FROM agent_task_queue previous
    LEFT JOIN autopilot_run previous_run ON previous_run.id = previous.autopilot_run_id
    WHERE previous.agent_id = q.agent_id AND previous.runtime_id = q.runtime_id
      AND previous.issue_id IS NOT DISTINCT FROM q.issue_id
      AND previous.chat_session_id IS NOT DISTINCT FROM q.chat_session_id
      AND (
          q.issue_id IS NOT NULL OR q.chat_session_id IS NOT NULL
          OR (ar.autopilot_id IS NOT NULL AND previous_run.autopilot_id = ar.autopilot_id)
          OR previous.id = q.id
      )
      AND previous.work_dir IS NOT NULL AND previous.work_dir <> ''
      AND COALESCE(previous.durable_work_dir, '') = ''
    ORDER BY previous.created_at DESC, previous.id DESC
    LIMIT 1
) current_work ON true
WHERE a.workspace_id = sqlc.arg(workspace_id)
  AND (sqlc.narg(runtime_id)::uuid IS NULL OR q.runtime_id = sqlc.narg(runtime_id))
  AND q.id = ANY(sqlc.arg(task_ids)::uuid[]);

-- name: ListMissingTaskGCIDs :many
-- Workspace access is checked before this query. Existing out-of-scope tasks
-- remain unknown; only genuinely deleted IDs receive a missing-task fact.
SELECT requested.id::uuid AS id
FROM unnest(sqlc.arg(task_ids)::uuid[]) AS requested(id)
LEFT JOIN agent_task_queue q ON q.id = requested.id
WHERE q.id IS NULL;
