CREATE INDEX CONCURRENTLY notification_bot_delivery_ready_idx ON notification_bot_delivery (next_attempt_at) WHERE completed_at IS NULL;
