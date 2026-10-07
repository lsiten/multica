CREATE TABLE application_access_ticket (
 token_hash text NOT NULL,
 endpoint_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 user_id uuid NOT NULL,
 endpoint_revision bigint NOT NULL,
 expires_at timestamptz NOT NULL DEFAULT now()+interval '1 minute'
);
