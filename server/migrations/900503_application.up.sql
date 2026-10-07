CREATE TABLE application (
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 workspace_id uuid NOT NULL,
 project_id uuid NOT NULL,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
 description text NOT NULL DEFAULT '',
 kind text NOT NULL CHECK (kind IN ('service','composition')),
 revision bigint NOT NULL DEFAULT 1,
 created_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE application_revision (
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 application_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 revision bigint NOT NULL,
 config jsonb NOT NULL,
 relations jsonb NOT NULL DEFAULT '[]',
 actor_type text NOT NULL CHECK (actor_type IN ('member','agent')),
 actor_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE application_relation (
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 workspace_id uuid NOT NULL,
 project_id uuid NOT NULL,
 source_id uuid NOT NULL,
 target_id uuid NOT NULL,
 type text NOT NULL CHECK (type IN ('contains','depends_on','related')),
 required boolean NOT NULL DEFAULT true,
 condition text NOT NULL DEFAULT '',
 start_external boolean NOT NULL DEFAULT false,
 CHECK (source_id <> target_id)
);

CREATE TABLE application_instance (
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 workspace_id uuid NOT NULL,
 application_id uuid NOT NULL,
 runtime_id uuid NOT NULL,
 daemon_id uuid NOT NULL,
 revision bigint NOT NULL,
 observed_revision bigint NOT NULL DEFAULT 0,
 generation bigint NOT NULL DEFAULT 0,
 desired_state text NOT NULL DEFAULT 'stopped' CHECK (desired_state IN ('running','stopped')),
 process_state text NOT NULL DEFAULT 'stopped',
 health_state text NOT NULL DEFAULT 'unknown',
 error text NOT NULL DEFAULT '',
 code_version text NOT NULL DEFAULT '',
 dirty boolean NOT NULL DEFAULT false,
 started_at timestamptz,
 observed_at timestamptz,
 metrics jsonb NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE application_endpoint (
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 workspace_id uuid NOT NULL,
 application_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
 entry_path text NOT NULL DEFAULT '/',
 visibility text NOT NULL DEFAULT 'workspace' CHECK (visibility IN ('workspace','private')),
 state text NOT NULL DEFAULT 'unpublished',
 token_hash text NOT NULL DEFAULT '',
 published_by uuid NOT NULL,
 revision bigint NOT NULL DEFAULT 1,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE application_operation (
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 workspace_id uuid NOT NULL,
 application_id uuid NOT NULL,
 action text NOT NULL CHECK (action IN ('start','stop','restart','publish','unpublish')),
 actor_type text NOT NULL CHECK (actor_type IN ('member','agent')),
 actor_id uuid NOT NULL,
 user_id uuid NOT NULL,
 task_id uuid,
 idempotency_key text NOT NULL,
 request_hash text NOT NULL,
 plan jsonb NOT NULL,
 state text NOT NULL DEFAULT 'queued',
 error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 completed_at timestamptz
);

CREATE TABLE application_operation_step (
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 workspace_id uuid NOT NULL,
 operation_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 application_id uuid NOT NULL,
 runtime_id uuid NOT NULL,
 generation bigint NOT NULL,
 wave integer NOT NULL,
 required boolean NOT NULL,
 action text NOT NULL,
 command jsonb NOT NULL,
 state text NOT NULL DEFAULT 'queued',
 error text NOT NULL DEFAULT '',
 claim_token uuid,
 claimed_at timestamptz,
 lease_until timestamptz,
 completed_at timestamptz
);

CREATE TABLE application_instance_consumer (
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 workspace_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 root_application_id uuid NOT NULL,
 root_runtime_id uuid NOT NULL,
 actor_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
