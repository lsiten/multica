CREATE TABLE IF NOT EXISTS jev_decision_log (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    task_id uuid NOT NULL,
    agent_id uuid NOT NULL,
    agent_name text NOT NULL,
    issue_identifier text NOT NULL DEFAULT '',
    tool text NOT NULL,
    source text NOT NULL,
    model text NOT NULL,
    result_class text NOT NULL,
    error_code text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL,
    duration_ms bigint NOT NULL DEFAULT 0,
    phase integer NOT NULL DEFAULT 0,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
