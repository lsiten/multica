-- Keep the chat/session that produced each content version so history can be
-- grouped by session instead of flattening every edit under its calendar day.
ALTER TABLE document_content_version
    ADD COLUMN IF NOT EXISTS session_id TEXT;
