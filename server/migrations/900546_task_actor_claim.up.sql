CREATE TABLE IF NOT EXISTS task_actor_claim (
 token_hash text NOT NULL,
 token_id uuid NOT NULL,
 task_id uuid NOT NULL,
 runtime_id uuid NOT NULL,
 dispatched_at timestamptz NOT NULL,
 agent_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 user_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
