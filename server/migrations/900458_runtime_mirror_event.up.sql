CREATE TABLE runtime_mirror_event (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL,
    runtime_id uuid NOT NULL,
    runtime_name text NOT NULL DEFAULT '',
    event text NOT NULL CHECK (event IN (
        'session_started',
        'session_answered',
        'session_failed',
        'viewer_started',
        'viewer_stopped'
    )),
    failure_reason text NOT NULL DEFAULT '',
    viewer_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
