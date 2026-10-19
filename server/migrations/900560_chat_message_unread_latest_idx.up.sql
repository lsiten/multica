-- Chat session lists count assistant messages after last_read_at and fetch the
-- latest non-command message. Partial indexes keep both scans small without
-- indexing channel-control rows that never appear in the UI transcript.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_chat_message_session_unread
    ON chat_message (chat_session_id, created_at DESC)
    WHERE role = 'assistant' AND message_kind <> 'channel_command';
