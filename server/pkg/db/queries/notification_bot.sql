-- name: ListNotificationBots :many
SELECT * FROM notification_bot WHERE workspace_id = $1 AND user_id = $2 ORDER BY created_at;

-- name: GetNotificationBot :one
SELECT * FROM notification_bot WHERE id = $1 AND workspace_id = $2 AND user_id = $3;

-- name: CreateNotificationBot :one
INSERT INTO notification_bot (workspace_id, user_id, name, platform, credentials, is_enabled)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: UpdateNotificationBot :one
UPDATE notification_bot SET name = $4,
    credentials = COALESCE(sqlc.narg('credentials')::bytea, credentials),
    is_enabled = $5,
    active_since = CASE WHEN is_enabled <> $5 THEN now() ELSE active_since END,
    updated_at = now(), last_error = ''
WHERE id = $1 AND workspace_id = $2 AND user_id = $3 RETURNING *;

-- name: DeleteNotificationBot :execrows
WITH removed AS (
    DELETE FROM notification_bot b WHERE b.id = $1 AND b.workspace_id = $2 AND b.user_id = $3 RETURNING b.id
), deliveries AS (
    DELETE FROM notification_bot_delivery WHERE bot_id IN (SELECT id FROM removed)
)
SELECT id FROM removed;

-- name: EnqueueNotificationBotDeliveries :exec
INSERT INTO notification_bot_delivery (bot_id, inbox_id)
SELECT b.id, i.id FROM notification_bot b
JOIN inbox_item i ON i.workspace_id = b.workspace_id AND i.recipient_id = b.user_id AND i.recipient_type = 'member'
WHERE b.is_enabled AND i.created_at >= b.active_since
    AND i.created_at > now() - interval '24 hours'
    AND EXISTS (SELECT 1 FROM member m WHERE m.workspace_id = b.workspace_id AND m.user_id = b.user_id)
    AND NOT EXISTS (SELECT 1 FROM notification_bot_delivery d WHERE d.bot_id = b.id AND d.inbox_id = i.id)
ORDER BY i.created_at LIMIT 200
ON CONFLICT (bot_id, inbox_id) DO NOTHING;

-- name: ClaimNotificationBotDelivery :one
WITH candidate AS (
    SELECT id FROM notification_bot_delivery
    WHERE completed_at IS NULL AND next_attempt_at <= now()
    ORDER BY next_attempt_at FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE notification_bot_delivery SET attempts = attempts + 1, next_attempt_at = now() + interval '1 minute'
WHERE id IN (SELECT id FROM candidate) RETURNING *;

-- name: GetNotificationBotDeliveryTarget :one
SELECT b.*, i.title, i.body, i.type AS inbox_type, i.issue_id,
       w.slug, COALESCE(p.preferences, '{}'::jsonb)::jsonb AS preferences
FROM notification_bot b
JOIN inbox_item i ON i.id = sqlc.arg('inbox_id') AND i.workspace_id = b.workspace_id AND i.recipient_id = b.user_id AND i.recipient_type = 'member'
JOIN workspace w ON w.id = b.workspace_id
LEFT JOIN notification_preference p ON p.workspace_id = b.workspace_id AND p.user_id = b.user_id
WHERE b.id = sqlc.arg('bot_id') AND b.is_enabled AND i.created_at >= b.active_since
    AND i.created_at > now() - interval '24 hours'
    AND EXISTS (SELECT 1 FROM member m WHERE m.workspace_id = b.workspace_id AND m.user_id = b.user_id);

-- name: FinishNotificationBotDelivery :exec
UPDATE notification_bot_delivery SET completed_at = CASE WHEN sqlc.arg('finished')::boolean THEN now() ELSE NULL END,
    next_attempt_at = now() + sqlc.arg('retry_seconds')::integer * interval '1 second'
WHERE id = $1 AND attempts = sqlc.arg('attempts');

-- name: RecordNotificationBotResult :exec
UPDATE notification_bot SET last_error = $2,
    last_delivery_at = CASE WHEN $2 = '' THEN now() ELSE last_delivery_at END WHERE id = $1;

-- name: PruneNotificationBotDeliveries :exec
DELETE FROM notification_bot_delivery WHERE created_at < now() - interval '2 days';
