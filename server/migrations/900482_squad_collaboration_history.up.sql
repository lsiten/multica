CREATE TABLE squad_collaboration_history (
    squad_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    revision integer NOT NULL,
    snapshot jsonb NOT NULL,
    updated_by uuid,
    created_at timestamptz NOT NULL DEFAULT now()
);
