-- name: CreateMeetingSession :one
INSERT INTO meeting_session (
    workspace_id, creator_id, source_type, source_chat_session_id, source_agent_id,
    runtime_id, status, language, timezone, audio_retention
)
VALUES ($1, $2, $3, $4, $5, $6, 'recording', $7, $8, 'discard')
RETURNING *;

-- name: GetMeetingSession :one
SELECT * FROM meeting_session
WHERE id = $1 AND workspace_id = $2;

-- name: ListMeetingSessions :many
SELECT * FROM meeting_session
WHERE workspace_id = sqlc.arg('workspace_id')::uuid
  AND (sqlc.arg('search')::text = '' OR title ILIKE '%' || sqlc.arg('search')::text || '%' OR topic ILIKE '%' || sqlc.arg('search')::text || '%' OR summary ILIKE '%' || sqlc.arg('search')::text || '%' OR transcript ILIKE '%' || sqlc.arg('search')::text || '%')
  AND (sqlc.arg('include_archived')::boolean OR archived_at IS NULL)
  AND (sqlc.narg('project_id')::uuid IS NULL OR project_id = sqlc.narg('project_id')::uuid)
  AND (sqlc.narg('agent_id')::uuid IS NULL OR source_agent_id = sqlc.narg('agent_id')::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit')::int;

-- name: UpdateMeetingSessionMetadata :one
UPDATE meeting_session
SET title = $3,
    topic = $4,
    project_id = $5,
    summary = $6,
    transcript = $7,
    decisions = $8,
    action_items = $9,
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: SetMeetingSessionArchived :one
UPDATE meeting_session
SET archived_at = $3,
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: DeleteMeetingDistributionsByMeeting :exec
DELETE FROM meeting_distribution WHERE meeting_id = $1 AND workspace_id = $2;

-- name: DeleteMeetingSegmentsByMeeting :exec
DELETE FROM meeting_segment
WHERE meeting_id = $1;

-- name: DeleteMeetingSession :exec
DELETE FROM meeting_session
WHERE id = $1 AND workspace_id = $2;

-- name: UpdateMeetingSessionStatus :one
UPDATE meeting_session
SET status = $3,
    ended_at = CASE WHEN $3 IN ('finalizing', 'organizing', 'ready', 'failed', 'cancelled') THEN COALESCE(ended_at, now()) ELSE ended_at END,
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: AppendMeetingSegment :one
INSERT INTO meeting_segment (meeting_id, sequence, start_ms, end_ms, speaker_label, text, is_final, confidence)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (meeting_id, sequence) DO UPDATE SET
    start_ms = EXCLUDED.start_ms,
    end_ms = EXCLUDED.end_ms,
    speaker_label = EXCLUDED.speaker_label,
    text = EXCLUDED.text,
    is_final = EXCLUDED.is_final,
    confidence = EXCLUDED.confidence
RETURNING *;

-- name: ListMeetingSegments :many
SELECT * FROM meeting_segment
WHERE meeting_id = $1
ORDER BY sequence ASC;

-- name: FinalizeMeetingSession :one
UPDATE meeting_session
SET status = $3,
    title = $4,
    topic = $5,
    project_id = $6,
    transcript = $7,
    summary = $8,
    decisions = $9,
    action_items = $10,
    classification = $11,
    ended_at = COALESCE(ended_at, now()),
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: CreateMeetingDistribution :one
INSERT INTO meeting_distribution (meeting_id, workspace_id, agent_id, sender_id, instruction, status, chat_session_id, task_id, error_message)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListMeetingDistributions :many
SELECT * FROM meeting_distribution
WHERE meeting_id = $1 AND workspace_id = $2
ORDER BY created_at DESC, id DESC;
