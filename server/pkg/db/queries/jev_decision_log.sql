-- name: UpsertJevDecisionLog :one
INSERT INTO jev_decision_log (id, workspace_id, task_id, agent_id, agent_name, issue_identifier, tool, source, model, result_class, error_code, started_at, duration_ms, phase, payload)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(task_id), sqlc.arg(agent_id), sqlc.arg(agent_name), sqlc.arg(issue_identifier), sqlc.arg(tool), sqlc.arg(source), sqlc.arg(model), sqlc.arg(result_class), sqlc.arg(error_code), sqlc.arg(started_at), sqlc.arg(duration_ms), sqlc.arg(phase), sqlc.arg(payload))
ON CONFLICT (id) DO UPDATE SET
    result_class = CASE WHEN EXCLUDED.phase > jev_decision_log.phase THEN EXCLUDED.result_class ELSE jev_decision_log.result_class END,
    error_code = CASE WHEN EXCLUDED.phase > jev_decision_log.phase THEN EXCLUDED.error_code ELSE jev_decision_log.error_code END,
    duration_ms = CASE WHEN EXCLUDED.phase > jev_decision_log.phase THEN EXCLUDED.duration_ms ELSE jev_decision_log.duration_ms END,
    payload = CASE WHEN EXCLUDED.phase > jev_decision_log.phase THEN EXCLUDED.payload ELSE jev_decision_log.payload END,
    phase = GREATEST(EXCLUDED.phase, jev_decision_log.phase)
WHERE jev_decision_log.workspace_id = EXCLUDED.workspace_id AND jev_decision_log.task_id = EXCLUDED.task_id AND jev_decision_log.started_at = EXCLUDED.started_at
RETURNING id;

-- name: ListJevDecisionLogs :many
SELECT id, task_id, agent_id, agent_name, issue_identifier, tool, source, model, result_class, error_code, started_at, duration_ms
FROM jev_decision_log
WHERE workspace_id = sqlc.arg(workspace_id) AND created_at <= sqlc.arg(as_of)::timestamptz
  AND (sqlc.arg(query)::text = '' OR concat_ws(' ', id::text, task_id::text, agent_id::text, agent_name, issue_identifier, model, tool, error_code, payload::text) ILIKE '%' || sqlc.arg(query)::text || '%')
  AND (sqlc.arg(result_class)::text = '' OR result_class = sqlc.arg(result_class))
  AND (sqlc.arg(source)::text = '' OR source = sqlc.arg(source))
  AND (sqlc.arg(agent_query)::text = '' OR concat_ws(' ', agent_id::text, agent_name) ILIKE '%' || sqlc.arg(agent_query)::text || '%')
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR started_at >= sqlc.narg(from_time))
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR started_at <= sqlc.narg(to_time))
ORDER BY started_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::integer OFFSET sqlc.arg(page_offset)::integer;

-- name: CountJevDecisionLogs :one
SELECT count(*) FROM jev_decision_log
WHERE workspace_id = sqlc.arg(workspace_id) AND created_at <= sqlc.arg(as_of)::timestamptz
  AND (sqlc.arg(query)::text = '' OR concat_ws(' ', id::text, task_id::text, agent_id::text, agent_name, issue_identifier, model, tool, error_code, payload::text) ILIKE '%' || sqlc.arg(query)::text || '%')
  AND (sqlc.arg(result_class)::text = '' OR result_class = sqlc.arg(result_class))
  AND (sqlc.arg(source)::text = '' OR source = sqlc.arg(source))
  AND (sqlc.arg(agent_query)::text = '' OR concat_ws(' ', agent_id::text, agent_name) ILIKE '%' || sqlc.arg(agent_query)::text || '%')
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR started_at >= sqlc.narg(from_time))
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR started_at <= sqlc.narg(to_time));

-- name: GetJevDecisionLog :one
SELECT * FROM jev_decision_log WHERE id = $1 AND workspace_id = $2;

-- name: DeleteWorkspaceJevDecisionLogs :exec
DELETE FROM jev_decision_log WHERE workspace_id = $1;
