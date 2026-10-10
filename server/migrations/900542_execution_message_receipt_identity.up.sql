CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS execution_message_receipt_identity_idx ON execution_message_receipt (execution_id, sequence);
