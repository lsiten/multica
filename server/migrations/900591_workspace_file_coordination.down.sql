-- A rollback refuses to discard the identity of retired or detached files.
ALTER TABLE workspace_file_resource DROP CONSTRAINT workspace_file_resource_state_check;
ALTER TABLE workspace_file_resource ADD CONSTRAINT workspace_file_resource_state_check CHECK (state IN ('active', 'trashed'));
DROP TABLE workspace_file_operation;
DROP TABLE workspace_file_host_state;
