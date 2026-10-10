CREATE TABLE IF NOT EXISTS application_service_authority (
 runtime_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 daemon_id text NOT NULL,
 owner_id uuid NOT NULL,
 member_id uuid NOT NULL,
 service_instance_id uuid NOT NULL,
 generation bigint NOT NULL CHECK (generation > 0),
 revoked boolean NOT NULL DEFAULT false,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS application_service_grant (
 token_hash text NOT NULL,
 runtime_id uuid NOT NULL,
 generation bigint NOT NULL,
 operations text[] NOT NULL,
 expires_at timestamptz NOT NULL
);
