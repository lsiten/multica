-- Shell chat badges read active sessions for one creator and order them by
-- pinned/activity state. Keep that access path independent from the archived
-- history query used by the dedicated Chat surface.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_chat_session_creator_active_list
    ON chat_session (workspace_id, creator_id, pinned_at DESC, updated_at DESC)
    WHERE status = 'active';
