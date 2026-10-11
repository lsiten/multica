-- name: InsertDocumentContentVersion :one
-- Appends one committed content state. The unique (owner_type, owner_id,
-- revision) index (migration 900550) rejects a duplicate version number; the
-- caller computes the next revision and retries on conflict.
INSERT INTO document_content_version (
    workspace_id, owner_type, owner_id, revision, content_type,
    content, content_url, size_bytes, actor_type, actor_id, note
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: ListDocumentContentVersions :many
-- Newest first, capped defensively: the diff/rollback UI is not built for more
-- than this many versions, and the unique index bounds real data well below it.
SELECT * FROM document_content_version
WHERE owner_type = $1
  AND owner_id = $2
  AND workspace_id = $3
ORDER BY revision DESC
LIMIT 200;

-- name: GetDocumentContentVersion :one
SELECT * FROM document_content_version
WHERE owner_type = $1
  AND owner_id = $2
  AND revision = $3
  AND workspace_id = $4;

-- name: GetLatestDocumentContentVersion :one
SELECT * FROM document_content_version
WHERE owner_type = $1
  AND owner_id = $2
  AND workspace_id = $3
ORDER BY revision DESC
LIMIT 1;

-- name: ApplyIssueDescriptionForContentVersion :one
-- Applies a content version to an issue's Markdown body. Used by the document
-- content version record/rollback path. The compare-and-swap guard
-- (expected_revision, NULL skips it) rejects a stale write; a mismatch returns
-- no rows, which the caller maps to a revision conflict.
UPDATE issue
SET description = sqlc.narg('description'),
    updated_at  = now(),
    revision    = revision + 1
WHERE id = sqlc.arg('id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND (sqlc.narg('expected_revision')::bigint IS NULL OR revision = sqlc.narg('expected_revision')::bigint)
RETURNING *;

-- name: UpdateDocumentContentVersionContent :one
-- Merge path (D4): collapse a consecutive edit from the same actor into the
-- latest version by overwriting its content in place instead of appending a new
-- revision, so an agent's rapid multi-edit sequence does not spam the log.
-- Matches the specific (owner, revision) row so a concurrent writer that took
-- the next revision is never clobbered.
UPDATE document_content_version
SET content      = sqlc.narg('content'),
    content_url  = sqlc.narg('content_url'),
    size_bytes   = sqlc.arg('size_bytes'),
    actor_type   = sqlc.arg('actor_type'),
    actor_id     = sqlc.arg('actor_id'),
    note         = sqlc.narg('note'),
    created_at   = now()
WHERE owner_type = sqlc.arg('owner_type')
  AND owner_id = sqlc.arg('owner_id')
  AND revision = sqlc.arg('revision')
  AND workspace_id = sqlc.arg('workspace_id')
RETURNING *;
