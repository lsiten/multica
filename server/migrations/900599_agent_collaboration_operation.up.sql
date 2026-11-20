-- Durable command receipts for Agent-to-Agent collaboration. The row is only
-- metadata and references existing comments/Runs; it is not a second queue or
-- transcript. Effects are inserted in the same transaction as this row.
CREATE TABLE agent_collaboration_operation (
    id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('member', 'agent', 'system')),
    actor_id UUID NOT NULL,
    client_request_id TEXT NOT NULL CHECK (length(btrim(client_request_id)) BETWEEN 1 AND 200),
    operation TEXT NOT NULL CHECK (operation IN ('delegate', 'append', 'next', 'watch', 'stop')),
    fingerprint TEXT NOT NULL CHECK (length(fingerprint) BETWEEN 1 AND 128),
    status TEXT NOT NULL CHECK (status IN ('accepted', 'provider_accepted', 'watching', 'stop_requested', 'completed', 'failed', 'uncertain')),
    source_task_id UUID,
    issue_id UUID,
    target_agent_id UUID,
    target_task_id UUID,
    comment_id UUID,
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
