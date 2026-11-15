-- Persist the autosave/checkpoint distinction. The API already exposes
-- `merged`, but deriving it from request memory made every history reload show
-- autosaves as checkpoints.
ALTER TABLE document_content_version
    ADD COLUMN IF NOT EXISTS merged BOOLEAN NOT NULL DEFAULT FALSE;
