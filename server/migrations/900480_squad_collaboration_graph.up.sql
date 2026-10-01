CREATE TABLE squad_collaboration_graph (
    squad_id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    revision integer NOT NULL DEFAULT 1,
    relations jsonb NOT NULL DEFAULT '[]'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);
