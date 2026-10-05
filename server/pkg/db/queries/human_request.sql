-- name: CreateHumanRequest :one
INSERT INTO human_request (id, workspace_id, source_task_id, agent_id, recipient_id,
    issue_id, chat_session_id, project_id, request_key, payload, expires_at, scope_fingerprint)
VALUES (@id, @workspace_id, @source_task_id, @agent_id, @recipient_id,
    @issue_id, @chat_session_id, @project_id, @request_key, @payload, @expires_at, @scope_fingerprint)
RETURNING *;

-- name: GetHumanRequest :one
SELECT * FROM human_request WHERE id = @id AND workspace_id = @workspace_id;

-- name: GetHumanRequestByKey :one
SELECT * FROM human_request WHERE workspace_id = @workspace_id
    AND source_task_id = @source_task_id AND request_key = @request_key;

-- name: LockHumanRequest :one
SELECT * FROM human_request WHERE id = @id AND workspace_id = @workspace_id FOR UPDATE;

-- name: ListHumanRequests :many
SELECT * FROM human_request WHERE workspace_id = @workspace_id
    AND (recipient_id = @member_id OR agent_id = @agent_id)
    AND (sqlc.narg('issue_id')::uuid IS NULL OR issue_id = sqlc.narg('issue_id'))
    AND (sqlc.narg('chat_session_id')::uuid IS NULL OR chat_session_id = sqlc.narg('chat_session_id'))
    AND (sqlc.narg('project_id')::uuid IS NULL OR project_id = sqlc.narg('project_id'))
ORDER BY (status = 'pending') DESC, created_at DESC LIMIT 100;

-- name: HasPendingHumanRequestForTask :one
SELECT EXISTS(SELECT 1 FROM human_request WHERE source_task_id = @task_id
  AND workspace_id = @workspace_id AND project_id = @project_id
  AND recipient_id = @recipient_id AND status = 'pending' AND expires_at > now())::boolean;

-- name: RespondHumanRequest :one
UPDATE human_request SET status = @status, response = @response,
    response_task_id = @response_task_id, responded_at = now(), updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND revision = @revision AND status = 'pending'
RETURNING *;

-- name: ReplaceHumanRequest :one
UPDATE human_request SET payload = @payload, scope_fingerprint = @scope_fingerprint, revision = revision + 1, expires_at = @expires_at, updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND status = 'pending'
RETURNING *;

-- name: UpdateHumanRequestInbox :one
UPDATE inbox_item SET title = @title, body = @body, read = false, archived = false, created_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND type = 'human_action_requested'
RETURNING *;

-- name: RequeueHumanRequestNotificationDeliveries :exec
-- Replace delivery IDs so an in-flight worker for an older revision cannot
-- finish or retry the new revision's delivery.
WITH removed AS (
    DELETE FROM notification_bot_delivery WHERE inbox_id = @inbox_id RETURNING bot_id
)
INSERT INTO notification_bot_delivery (bot_id, inbox_id)
SELECT bot_id, @inbox_id FROM removed;

-- name: HasHumanRequestHandoffForTask :one
SELECT EXISTS(SELECT 1 FROM human_request WHERE source_task_id = @task_id AND workspace_id = @workspace_id
    AND ((status = 'pending' AND expires_at > now())
        OR (status IN ('answered', 'declined') AND response_task_id IS NOT NULL)))::boolean;

-- name: CancelHumanRequestsByIssue :exec
UPDATE human_request SET status = 'cancelled', updated_at = now()
WHERE workspace_id = @workspace_id AND issue_id = @issue_id AND status = 'pending';

-- name: CancelHumanRequestsByChat :exec
UPDATE human_request SET status = 'cancelled', updated_at = now()
WHERE workspace_id = @workspace_id AND chat_session_id = @chat_session_id AND status = 'pending';

-- name: SetCommentHumanRequest :exec
UPDATE comment SET human_request_id = @request_id WHERE id = @request_id AND workspace_id = @workspace_id;

-- name: SetChatMessageHumanRequest :exec
UPDATE chat_message SET human_request_id = @request_id WHERE id = @request_id AND chat_session_id = @chat_session_id;

-- name: UpdateHumanRequestComment :one
WITH changed AS (
    UPDATE comment AS c SET content = @content, revision = c.revision + 1, updated_at = now()
    WHERE c.human_request_id = @request_id AND c.workspace_id = @workspace_id AND c.deleted_at IS NULL
    RETURNING c.*
), touched AS (
    UPDATE issue SET revision = issue.revision + 1,
        last_activity_at = GREATEST(COALESCE(issue.last_activity_at, issue.updated_at), now())
    FROM changed WHERE issue.id = changed.issue_id AND issue.workspace_id = changed.workspace_id
    RETURNING issue.id
)
SELECT changed.* FROM changed CROSS JOIN (SELECT count(*) FROM touched) AS owner_update;

-- name: UpdateHumanRequestMessage :exec
UPDATE chat_message SET content = @content
WHERE human_request_id = @request_id AND chat_session_id = @chat_session_id;

-- name: SetHumanResponseTaskContext :one
UPDATE agent_task_queue SET context = COALESCE(context, '{}'::jsonb) || jsonb_build_object(
    'human_response', jsonb_build_object('request_id', @request_id::text, 'revision', @revision::bigint,
    'source_task_id', @source_task_id::text, 'prompt', @prompt::text))
WHERE id = @id RETURNING *;

-- name: ExpireHumanRequests :exec
UPDATE human_request r SET status = CASE WHEN expires_at <= now() THEN 'expired' ELSE 'cancelled' END, updated_at = now()
WHERE r.workspace_id = @workspace_id AND r.status = 'pending'
  AND (r.expires_at <= now()
    OR NOT EXISTS (SELECT 1 FROM agent_task_queue t WHERE t.id = r.source_task_id AND t.status NOT IN ('failed', 'cancelled') AND t.issue_id IS NOT DISTINCT FROM r.issue_id AND t.chat_session_id IS NOT DISTINCT FROM r.chat_session_id)
    OR NOT EXISTS (SELECT 1 FROM agent a WHERE a.id = r.agent_id AND a.workspace_id = r.workspace_id AND a.archived_at IS NULL)
    OR (r.issue_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM issue i WHERE i.id = r.issue_id AND i.workspace_id = r.workspace_id AND i.status NOT IN ('done', 'cancelled')))
    OR (r.chat_session_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM chat_session c WHERE c.id = r.chat_session_id AND c.workspace_id = r.workspace_id AND c.status = 'active'))
    OR (r.issue_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM comment c WHERE c.human_request_id = r.id AND c.deleted_at IS NULL)));

-- name: DeleteHumanRequestsByWorkspace :exec
DELETE FROM human_request WHERE workspace_id = @workspace_id;

-- name: CancelStaleHumanRequest :one
UPDATE human_request SET status = 'cancelled', updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND revision = @revision
  AND scope_fingerprint = @scope_fingerprint AND status = 'pending'
RETURNING *;

-- name: ListHumanResponseReceipts :many
SELECT * FROM human_request WHERE workspace_id = @workspace_id AND status = 'answered'
AND response->'origin'->>'reply_id' = ANY(@reply_ids::text[]);
