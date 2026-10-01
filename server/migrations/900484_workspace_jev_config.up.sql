CREATE TABLE workspace_jev_config (
 workspace_id uuid NOT NULL,
 config jsonb NOT NULL,
 revision bigint NOT NULL DEFAULT 1,
 updated_at timestamptz NOT NULL DEFAULT now()
);
