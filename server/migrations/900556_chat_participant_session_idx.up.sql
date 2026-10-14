-- List a group's members by session. Own single-statement file: CONCURRENTLY
-- cannot run inside a transaction (AGENTS.md).
CREATE INDEX CONCURRENTLY idx_chat_participant_session ON chat_participant(chat_session_id);
