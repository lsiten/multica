CREATE INDEX CONCURRENTLY workspace_file_operation_pending ON workspace_file_operation (workspace_id, host_key) WHERE state = 'pending';
