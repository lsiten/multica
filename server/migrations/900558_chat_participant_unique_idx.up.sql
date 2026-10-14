-- A member cannot be added to a group twice. Own single-statement file:
-- CONCURRENTLY cannot run inside a transaction (AGENTS.md).
CREATE UNIQUE INDEX CONCURRENTLY idx_chat_participant_session_member
    ON chat_participant (chat_session_id, member_type, member_id);
