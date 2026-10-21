-- name: UploadCleanupInventory :many
-- Operator-wide maintenance inventory, deliberately read from the primary.
-- An old status alone is insufficient: live runs keep their owner's files.
WITH expired_issue AS MATERIALIZED (
    SELECT i.id FROM issue i
    WHERE i.updated_at < sqlc.arg(cutoff)::timestamptz
      AND (sqlc.narg(scope)::uuid IS NULL OR i.workspace_id = sqlc.narg(scope)::uuid)
      AND (i.status = 'cancelled' OR EXISTS (
        SELECT 1 FROM issue_status s
        WHERE s.workspace_id = i.workspace_id AND s.key = i.status AND s.category = 'closed'
      ))
      AND NOT EXISTS (
        SELECT 1 FROM agent_task_queue t WHERE t.issue_id = i.id
          AND t.status NOT IN ('completed', 'failed', 'cancelled')
      )
), expired_chat AS MATERIALIZED (
    SELECT c.id FROM chat_session c
    WHERE c.status = 'archived' AND c.updated_at < sqlc.arg(cutoff)::timestamptz
      AND (sqlc.narg(scope)::uuid IS NULL OR c.workspace_id = sqlc.narg(scope)::uuid)
      AND NOT EXISTS (
        SELECT 1 FROM agent_task_queue t WHERE t.chat_session_id = c.id
          AND t.status NOT IN ('completed', 'failed', 'cancelled')
      )
), expired_run AS MATERIALIZED (
    SELECT t.id FROM agent_task_queue t
    WHERE t.issue_id IN (SELECT id FROM expired_issue)
       OR t.chat_session_id IN (SELECT id FROM expired_chat)
), eligible AS MATERIALIZED (
    SELECT a.*, CASE WHEN a.issue_id IS NULL AND a.chat_message_id IS NULL
      THEN 'unbound' ELSE 'expired_owner' END::text AS cleanup_reason FROM attachment a
    WHERE a.source_context_id IS NULL AND a.created_at < sqlc.arg(cutoff)::timestamptz
      AND (sqlc.narg(scope)::uuid IS NULL OR a.workspace_id = sqlc.narg(scope)::uuid)
      AND (a.comment_id IS NULL OR EXISTS (
        SELECT 1 FROM comment c WHERE c.id = a.comment_id AND c.workspace_id = a.workspace_id
          AND c.issue_id IN (SELECT id FROM expired_issue)
      ))
      AND (a.chat_message_id IS NULL OR EXISTS (
        SELECT 1 FROM chat_message m WHERE m.id = a.chat_message_id
          AND m.chat_session_id IN (SELECT id FROM expired_chat)
      ))
      AND (a.task_id IS NULL OR EXISTS (
        SELECT 1 FROM agent_task_queue t WHERE t.id = a.task_id
          AND t.status IN ('completed', 'failed', 'cancelled') AND t.completed_at < sqlc.arg(cutoff)::timestamptz
      ))
      AND (a.issue_id IN (SELECT id FROM expired_issue)
        OR (a.issue_id IS NULL AND a.comment_id IS NULL AND a.chat_session_id IN (SELECT id FROM expired_chat))
        OR (a.issue_id IS NULL AND a.comment_id IS NULL AND a.chat_message_id IS NULL
        ))
), documents AS (
    SELECT to_jsonb(a)::text AS content FROM attachment a WHERE a.id NOT IN (SELECT id FROM eligible)
    UNION ALL SELECT to_jsonb(i)::text FROM issue i WHERE i.id NOT IN (SELECT id FROM expired_issue)
    UNION ALL SELECT to_jsonb(c)::text FROM comment c WHERE c.issue_id NOT IN (SELECT id FROM expired_issue)
    UNION ALL SELECT to_jsonb(m)::text FROM chat_message m WHERE m.chat_session_id NOT IN (SELECT id FROM expired_chat)
    UNION ALL SELECT to_jsonb(t)::text FROM agent_task_queue t WHERE t.id NOT IN (SELECT id FROM expired_run)
    UNION ALL SELECT to_jsonb(m)::text FROM task_message m WHERE m.task_id NOT IN (SELECT id FROM expired_run)
    UNION ALL SELECT to_jsonb(d)::text FROM chat_draft_restore d WHERE d.chat_session_id NOT IN (SELECT id FROM expired_chat)
    UNION ALL SELECT to_jsonb(v)::text FROM document_content_version v
      WHERE NOT (v.owner_type = 'issue' AND v.owner_id IN (SELECT id FROM expired_issue))
        AND NOT (v.owner_type = 'attachment' AND v.owner_id IN (SELECT id FROM eligible))
    UNION ALL SELECT to_jsonb(u)::text FROM "user" u
    UNION ALL SELECT to_jsonb(w)::text FROM workspace w
    UNION ALL SELECT to_jsonb(a)::text FROM agent a
    UNION ALL SELECT to_jsonb(s)::text FROM squad s
    UNION ALL SELECT to_jsonb(p)::text FROM project p
    UNION ALL SELECT to_jsonb(p)::text FROM project_resource p
    UNION ALL SELECT to_jsonb(s)::text FROM issue_source_context s
    UNION ALL SELECT to_jsonb(p)::text FROM plugin_storage p
    UNION ALL SELECT to_jsonb(s)::text FROM skill s
    UNION ALL SELECT to_jsonb(s)::text FROM skill_file s
    UNION ALL SELECT to_jsonb(a)::text FROM application a
    UNION ALL SELECT to_jsonb(a)::text FROM application_revision a
    UNION ALL SELECT to_jsonb(a)::text FROM autopilot a
    UNION ALL SELECT to_jsonb(a)::text FROM autopilot_rule_version a
    UNION ALL SELECT to_jsonb(g)::text FROM issue_goal g WHERE g.issue_id NOT IN (SELECT id FROM expired_issue)
    UNION ALL SELECT to_jsonb(w)::text FROM issue_wakeup w WHERE w.enabled
    UNION ALL SELECT to_jsonb(q)::text FROM quick_action q
    UNION ALL SELECT to_jsonb(h)::text FROM human_request h
    UNION ALL SELECT to_jsonb(d)::text FROM agent_builder_draft d
    UNION ALL SELECT '/uploads/' || storage_key FROM channel_media_pending_object
    UNION ALL SELECT '/uploads/' || storage_key FROM issue_source_context_object_intent
)
SELECT id AS attachment_id, workspace_id, url, size_bytes, cleanup_reason, ''::text AS reference
FROM eligible
UNION ALL
SELECT NULL::uuid, NULL::uuid, ''::text, 0::bigint, ''::text, content FROM documents
WHERE content LIKE '%/uploads/%' OR content LIKE '%http%' OR content LIKE '%attachment%';

-- name: LockUploadCleanupReferences :exec
-- Keep restores, attachment binding and new references out of the short
-- reference-check/delete window. A lock timeout skips a busy round.
LOCK TABLE agent, agent_builder_draft, agent_task_queue, application, application_revision,
    attachment, autopilot, autopilot_rule_version,
    channel_media_pending_object, chat_draft_restore, chat_message, chat_session,
    comment, document_content_version, human_request, issue, issue_goal, issue_wakeup, issue_source_context,
    issue_source_context_object_intent, issue_status, plugin_storage, project,
    project_resource, quick_action, skill, skill_file, squad, task_message, "user", workspace
IN SHARE ROW EXCLUSIVE MODE;

-- name: DeletePurgedUploadAttachment :exec
-- Parent lifecycle and shared references were checked under the maintenance lock.
WITH deleted AS (
    DELETE FROM attachment WHERE attachment.id = $1 AND attachment.workspace_id = $2
    RETURNING attachment.issue_id, attachment.comment_id
), bumped_issue AS (
    UPDATE issue SET revision = issue.revision + 1
    WHERE issue.workspace_id = $2 AND issue.id IN (SELECT deleted.issue_id FROM deleted)
)
UPDATE comment SET revision = comment.revision + 1
WHERE comment.workspace_id = $2 AND comment.id IN (SELECT deleted.comment_id FROM deleted);
