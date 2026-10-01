CREATE TABLE project_graph_event (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    task_id uuid,
    event_type text NOT NULL,
    node_id text,
    data jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
