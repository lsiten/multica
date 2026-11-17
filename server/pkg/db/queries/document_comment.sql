-- name: CreateDocumentComment :one
INSERT INTO document_comment (id, workspace_id, resource_key, resource_ref, parent_id, anchor, content, author_id)
VALUES (COALESCE(sqlc.narg('id')::uuid, gen_random_uuid()), $1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: GetMatchingDocumentCommentReceipt :one
SELECT * FROM document_comment
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id')
  AND resource_key = sqlc.arg('resource_key') AND author_id = sqlc.arg('author_id')
  AND content = sqlc.arg('content')
  AND parent_id IS NOT DISTINCT FROM sqlc.narg('parent_id')::uuid
  AND anchor IS NOT DISTINCT FROM sqlc.narg('anchor')::jsonb;

-- name: ListDocumentComments :many
SELECT note.* FROM document_comment AS note
LEFT JOIN document_comment AS root ON root.id = note.parent_id AND root.workspace_id = note.workspace_id
WHERE note.workspace_id = $1 AND note.resource_key = $2
ORDER BY COALESCE(root.created_at, note.created_at) DESC, COALESCE(note.parent_id, note.id) DESC,
         (note.parent_id IS NULL) DESC, note.created_at, note.id
LIMIT $3 OFFSET $4;

-- name: LockDocumentComment :one
SELECT * FROM document_comment
WHERE id = $1 AND workspace_id = $2 AND resource_key = $3
FOR UPDATE;

-- name: UpdateDocumentCommentResolution :one
UPDATE document_comment
SET resolved_at = CASE WHEN sqlc.arg('resolved')::boolean THEN now() ELSE NULL END,
    resolved_by = CASE WHEN sqlc.arg('resolved')::boolean THEN sqlc.arg('actor_id')::uuid ELSE NULL END,
    revision = revision + 1,
    updated_at = now()
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id')
  AND parent_id IS NULL
  AND revision = sqlc.arg('expected_revision')
RETURNING *;

-- name: DeleteDocumentCommentsForResource :exec
DELETE FROM document_comment WHERE workspace_id = $1 AND resource_key = $2;

-- name: DeleteDocumentCommentsForWorkspace :exec
DELETE FROM document_comment WHERE workspace_id = $1;

-- name: LockDocumentCommentIssue :one
SELECT id FROM issue WHERE id = $1 AND workspace_id = $2 FOR KEY SHARE;

-- name: LockDocumentCommentTask :one
SELECT task.id FROM agent_task_queue AS task
JOIN agent AS owner ON owner.id = task.agent_id
WHERE task.id = $1 AND owner.workspace_id = $2
FOR KEY SHARE OF task, owner;

-- name: LockDocumentCommentChat :one
SELECT id FROM chat_session WHERE id = $1 AND workspace_id = $2 FOR KEY SHARE;

-- name: LockDocumentCommentProjectResource :one
SELECT id FROM project_resource WHERE id = $1 AND workspace_id = $2 FOR KEY SHARE;

-- name: LockDocumentCommentProject :one
SELECT id FROM project WHERE id = $1 AND workspace_id = $2 FOR KEY SHARE;

-- name: LockDocumentCommentProjectResourceForDelete :one
SELECT id FROM project_resource WHERE id = $1 AND workspace_id = $2 FOR UPDATE;

-- name: DeleteDocumentCommentsForIssue :exec
DELETE FROM document_comment AS note
WHERE note.workspace_id = sqlc.arg('workspace_id') AND (
  note.resource_key = 'issue:' || sqlc.arg('issue_id')::uuid::text
  OR (note.resource_ref->>'fileId' IS NULL AND note.resource_ref->>'taskId' IN (SELECT task.id::text FROM agent_task_queue task WHERE task.issue_id = sqlc.arg('issue_id')))
  OR (note.resource_ref->>'ownerKind' = 'issue' AND note.resource_ref->>'ownerId' = sqlc.arg('issue_id')::uuid::text)
  OR note.resource_key IN (
    SELECT 'attachment:' || a.id::text FROM attachment a
    LEFT JOIN comment c ON c.id = a.comment_id AND c.workspace_id = a.workspace_id
    LEFT JOIN issue_source_context s ON s.id = a.source_context_id AND s.workspace_id = a.workspace_id
    WHERE a.workspace_id = sqlc.arg('workspace_id')
      AND (a.issue_id = sqlc.arg('issue_id') OR c.issue_id = sqlc.arg('issue_id') OR s.issue_id = sqlc.arg('issue_id'))
  )
);

-- name: DeleteDocumentCommentsForCommentAttachments :exec
DELETE FROM document_comment AS note
WHERE note.workspace_id = sqlc.arg('workspace_id') AND note.resource_key IN (
  SELECT 'attachment:' || a.id::text FROM attachment a
  WHERE a.workspace_id = sqlc.arg('workspace_id') AND a.comment_id = sqlc.arg('comment_id')
);

-- name: DeleteDocumentCommentsForProject :exec
DELETE FROM document_comment AS note
WHERE note.workspace_id = sqlc.arg('workspace_id')
  AND note.resource_ref->>'projectResourceId' IN (
    SELECT resource.id::text FROM project_resource resource
    WHERE resource.workspace_id = sqlc.arg('workspace_id') AND resource.project_id = sqlc.arg('project_id')
  );

-- name: DeleteDocumentCommentsForProjectResource :exec
DELETE FROM document_comment
WHERE workspace_id = $1 AND resource_ref->>'projectResourceId' = sqlc.arg('resource_id')::uuid::text;

-- name: DeleteDocumentCommentsForFileOwner :exec
DELETE FROM document_comment
WHERE workspace_id = $1 AND resource_ref->>'ownerKind' = sqlc.arg('owner_kind')::text
  AND resource_ref->>'ownerId' = sqlc.arg('owner_id')::uuid::text;
