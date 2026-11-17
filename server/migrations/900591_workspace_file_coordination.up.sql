CREATE TABLE workspace_file_host_state (
    workspace_id uuid NOT NULL,
    host_key text NOT NULL CHECK (length(host_key) = 64),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0)
);

CREATE TABLE workspace_file_operation (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL,
    host_key text NOT NULL CHECK (length(host_key) = 64),
    physical_key text NOT NULL CHECK (length(physical_key) = 64),
    directory_key text NOT NULL CHECK (length(directory_key) = 64),
    root_path text NOT NULL CHECK (length(root_path) BETWEEN 1 AND 4096),
    command_id text NOT NULL CHECK (length(command_id) BETWEEN 1 AND 128),
    actor_id uuid NOT NULL,
    request_hash text NOT NULL CHECK (length(request_hash) = 64),
    operation jsonb NOT NULL,
    binding_ref jsonb NOT NULL,
    resources jsonb NOT NULL CHECK (jsonb_typeof(resources) = 'array'),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'completed', 'rejected')),
    result jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK ((state = 'pending' AND result IS NULL) OR (state <> 'pending' AND result IS NOT NULL))
);

ALTER TABLE workspace_file_resource DROP CONSTRAINT workspace_file_resource_state_check;
ALTER TABLE workspace_file_resource ADD CONSTRAINT workspace_file_resource_state_check
    CHECK (state IN ('active', 'trashed', 'retired', 'detached'));
