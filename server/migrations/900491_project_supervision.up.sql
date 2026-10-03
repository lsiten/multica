CREATE TABLE project_supervision (
 project_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 configured_by uuid NOT NULL,
 enabled boolean NOT NULL DEFAULT false,
 config jsonb NOT NULL DEFAULT '{}',
 revision bigint NOT NULL DEFAULT 1,
 dirty_version bigint NOT NULL DEFAULT 1,
 handled_version bigint NOT NULL DEFAULT 0,
 next_check_at timestamptz NOT NULL DEFAULT now(),
 last_checked_at timestamptz,
 last_task_id uuid,
 last_fingerprint text NOT NULL DEFAULT '',
 no_progress_count integer NOT NULL DEFAULT 0,
 last_reason text NOT NULL DEFAULT '',
 last_result jsonb NOT NULL DEFAULT '{}',
 updated_at timestamptz NOT NULL DEFAULT now()
);
