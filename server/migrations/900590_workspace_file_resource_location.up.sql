CREATE UNIQUE INDEX CONCURRENTLY workspace_file_resource_location ON workspace_file_resource (workspace_id, directory_key, path_key) WHERE state = 'active';
