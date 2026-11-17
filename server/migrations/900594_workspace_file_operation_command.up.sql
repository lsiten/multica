CREATE UNIQUE INDEX CONCURRENTLY workspace_file_operation_command ON workspace_file_operation (workspace_id, physical_key, command_id);
