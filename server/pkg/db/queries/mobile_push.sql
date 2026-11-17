-- name: RegisterMobilePushDevice :one
INSERT INTO mobile_push_device (user_id, token, topic, environment)
VALUES ($1, $2, $3, $4)
ON CONFLICT (topic, environment, token) DO UPDATE SET
    user_id = EXCLUDED.user_id,
    active_since = CASE WHEN mobile_push_device.user_id = EXCLUDED.user_id THEN mobile_push_device.active_since ELSE now() END,
    updated_at = now()
RETURNING *;

-- name: DeleteMobilePushDevice :exec
DELETE FROM mobile_push_device
WHERE user_id = $1 AND token = $2 AND topic = $3 AND environment = $4;

-- name: InvalidateMobilePushDevice :exec
DELETE FROM mobile_push_device
WHERE id = $1 AND updated_at = $2;

-- name: EnqueueMobilePushDeliveries :exec
INSERT INTO mobile_push_delivery (device_id, inbox_id)
SELECT d.id, i.id FROM mobile_push_device d
JOIN inbox_item i ON i.recipient_id = d.user_id AND i.recipient_type = 'member'
WHERE i.created_at >= d.active_since AND i.created_at > now() - interval '24 hours'
    AND NOT i.read AND NOT i.archived
    AND EXISTS (SELECT 1 FROM member m WHERE m.workspace_id = i.workspace_id AND m.user_id = d.user_id)
    AND NOT EXISTS (SELECT 1 FROM mobile_push_delivery p WHERE p.device_id = d.id AND p.inbox_id = i.id)
ORDER BY i.created_at LIMIT 200
ON CONFLICT (device_id, inbox_id) DO NOTHING;

-- name: ClaimMobilePushDelivery :one
WITH candidate AS (
    SELECT id FROM mobile_push_delivery
    WHERE completed_at IS NULL AND next_attempt_at <= now()
    ORDER BY next_attempt_at FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE mobile_push_delivery SET attempts = attempts + 1, next_attempt_at = now() + interval '1 minute'
WHERE id IN (SELECT id FROM candidate) RETURNING *;

-- name: GetMobilePushDeliveryTarget :one
SELECT d.*, i.id AS inbox_item_id, i.title, i.body, i.type AS inbox_type, i.issue_id, i.details, i.workspace_id,
       w.slug, COALESCE(p.preferences, '{}'::jsonb)::jsonb AS preferences, iss.project_id AS issue_project_id, member.role,
       NULL::uuid AS chat_session_id, NULL::uuid AS chat_message_id, NULL::uuid AS task_id
FROM mobile_push_device d
JOIN inbox_item i ON i.id = sqlc.arg('inbox_id') AND i.recipient_id = d.user_id AND i.recipient_type = 'member'
JOIN workspace w ON w.id = i.workspace_id
JOIN member ON member.workspace_id = i.workspace_id AND member.user_id = d.user_id
LEFT JOIN issue iss ON iss.id = i.issue_id AND iss.workspace_id = i.workspace_id
LEFT JOIN notification_preference p ON p.workspace_id = i.workspace_id AND p.user_id = d.user_id
WHERE d.id = sqlc.arg('device_id') AND i.created_at >= d.active_since
    AND i.created_at > now() - interval '24 hours' AND NOT i.read AND NOT i.archived
    AND (i.issue_id IS NULL OR iss.id IS NOT NULL);

-- name: EnqueueMobileChatPushDeliveries :exec
INSERT INTO mobile_push_delivery (device_id, chat_message_id)
SELECT d.id, m.id FROM mobile_push_device d
JOIN chat_session s ON s.creator_id = d.user_id AND s.kind = 'direct' AND s.status = 'active' AND NOT s.is_agent_intro
JOIN chat_message m ON m.chat_session_id = s.id AND m.role = 'assistant' AND m.message_kind = 'message'
JOIN agent_task_queue t ON t.id = m.task_id AND t.completed_at IS NOT NULL
WHERE m.created_at >= d.active_since AND m.created_at > now() - interval '24 hours'
    AND m.created_at > s.last_read_at
    AND EXISTS (SELECT 1 FROM member member WHERE member.workspace_id = s.workspace_id AND member.user_id = d.user_id)
    AND NOT EXISTS (SELECT 1 FROM mobile_push_delivery p WHERE p.device_id = d.id AND p.chat_message_id = m.id)
ORDER BY m.created_at LIMIT 200
ON CONFLICT (device_id, chat_message_id) DO NOTHING;

-- name: GetMobileChatPushDeliveryTarget :one
SELECT d.*, NULL::uuid AS inbox_item_id, COALESCE(NULLIF(BTRIM(s.title), ''), 'Chat reply')::text AS title,
       m.content::text AS body, 'chat_reply'::text AS inbox_type, NULL::uuid AS issue_id,
       '{}'::jsonb AS details, s.workspace_id, w.slug, COALESCE(p.preferences, '{}'::jsonb)::jsonb AS preferences,
       s.project_id AS issue_project_id, member.role, s.id AS chat_session_id, m.id AS chat_message_id, m.task_id
FROM mobile_push_device d
JOIN chat_message m ON m.id = sqlc.arg('chat_message_id') AND m.role = 'assistant' AND m.message_kind = 'message'
JOIN chat_session s ON s.id = m.chat_session_id AND s.creator_id = d.user_id AND s.kind = 'direct' AND s.status = 'active' AND NOT s.is_agent_intro
JOIN agent_task_queue t ON t.id = m.task_id AND t.completed_at IS NOT NULL
JOIN workspace w ON w.id = s.workspace_id
JOIN member ON member.workspace_id = s.workspace_id AND member.user_id = d.user_id
LEFT JOIN notification_preference p ON p.workspace_id = s.workspace_id AND p.user_id = d.user_id
WHERE d.id = sqlc.arg('device_id') AND m.created_at >= d.active_since
    AND m.created_at > now() - interval '24 hours' AND m.created_at > s.last_read_at;

-- name: FinishMobilePushDelivery :exec
UPDATE mobile_push_delivery SET completed_at = CASE WHEN sqlc.arg('finished')::boolean THEN now() ELSE NULL END,
    next_attempt_at = now() + sqlc.arg('retry_seconds')::integer * interval '1 second'
WHERE id = $1 AND attempts = sqlc.arg('attempts');

-- name: PruneMobilePushDeliveries :exec
DELETE FROM mobile_push_delivery p WHERE p.created_at < now() - interval '2 days'
    OR NOT EXISTS (SELECT 1 FROM mobile_push_device d WHERE d.id = p.device_id)
    OR (p.inbox_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM inbox_item i WHERE i.id = p.inbox_id))
    OR (p.chat_message_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM chat_message m WHERE m.id = p.chat_message_id));
