-- Unique identity of a document's version history. Kept in its own single
-- statement file because the runner executes migrations outside an explicit
-- transaction, so CONCURRENTLY is required (AGENTS.md).
CREATE UNIQUE INDEX CONCURRENTLY document_content_version_owner_revision_idx
    ON document_content_version (owner_type, owner_id, revision);
