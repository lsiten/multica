-- Document content version log.
--
-- A "document" is a content-bearing entity a chat conversation can read and
-- update: an issue (its description IS the Markdown body) or an attachment
-- (a binary blob, e.g. an image or a PDF). Each row is one committed content
-- state, so the write side can show diffs and roll back without a second copy
-- of every body living in the task queue (see agent_task_queue.issue_snapshot,
-- which is change-detection only, not a version history).
--
-- `content` holds text versions (Markdown/HTML); `content_url` holds the blob
-- reference for binary versions; exactly one is populated. Binary files are
-- never rewritten in place -- a change produces a new row (and, for an
-- attachment, a new blob), while the prior row is kept for rollback.
--
-- owner_type/owner_id identify the document; revision is its monotonic version
-- number. (owner_type, owner_id, revision) is unique (see the index migration).
CREATE TABLE document_content_version (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID NOT NULL,
    owner_type    TEXT NOT NULL CHECK (owner_type IN ('issue', 'attachment')),
    owner_id      UUID NOT NULL,
    revision      BIGINT NOT NULL CHECK (revision > 0),
    content_type  TEXT NOT NULL,
    content       TEXT,
    content_url   TEXT,
    size_bytes    BIGINT NOT NULL DEFAULT 0,
    actor_type    TEXT NOT NULL CHECK (actor_type IN ('member', 'agent')),
    actor_id      UUID NOT NULL,
    note          TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
