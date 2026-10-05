CREATE INDEX CONCURRENTLY IF NOT EXISTS human_request_recipient_pending ON human_request (workspace_id, recipient_id, created_at DESC) WHERE status = 'pending';
