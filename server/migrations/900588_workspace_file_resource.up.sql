CREATE TABLE workspace_file_resource (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL,
    directory_key text NOT NULL,
    path_key text NOT NULL,
    relative_path text NOT NULL,
    directory boolean NOT NULL,
    binding_ref jsonb NOT NULL,
    state text NOT NULL DEFAULT 'active',
    recovery_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (length(directory_key) = 64),
    CHECK (length(path_key) = 64),
    CHECK (length(relative_path) BETWEEN 1 AND 4096),
    CHECK (state IN ('active', 'trashed'))
);
