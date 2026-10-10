CREATE TABLE IF NOT EXISTS runtime_supervisor (
 runtime_id uuid NOT NULL, workspace_id uuid NOT NULL, daemon_id text NOT NULL,
 instance_id uuid NOT NULL, epoch bigint NOT NULL CHECK (epoch > 0)
);
CREATE TABLE IF NOT EXISTS task_execution (
 task_id uuid NOT NULL, execution_id uuid NOT NULL, runtime_id uuid NOT NULL,
 workspace_id uuid NOT NULL, daemon_id text NOT NULL, worker_id uuid NOT NULL,
 dispatched_at timestamptz NOT NULL, supervisor_epoch bigint NOT NULL,
 revoked boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS execution_grant (
 token_hash text NOT NULL, task_id uuid NOT NULL, execution_id uuid NOT NULL,
 operations text[] NOT NULL, expires_at timestamptz NOT NULL, revoked boolean NOT NULL DEFAULT false
);
CREATE TABLE IF NOT EXISTS execution_snapshot (
 runtime_id uuid NOT NULL, snapshot_id uuid NOT NULL, supervisor_epoch bigint NOT NULL,
 next_page integer NOT NULL DEFAULT 0, pages jsonb NOT NULL DEFAULT '[]',
 complete boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT now()
);
