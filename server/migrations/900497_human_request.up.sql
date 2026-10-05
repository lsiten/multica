CREATE TABLE human_request (
    id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    source_task_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    recipient_id UUID NOT NULL,
    issue_id UUID,
    chat_session_id UUID,
    project_id UUID,
    request_key TEXT NOT NULL,
    payload JSONB NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'pending',
    response JSONB,
    response_task_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    responded_at TIMESTAMPTZ
);

ALTER TABLE comment ADD COLUMN human_request_id UUID;
ALTER TABLE chat_message ADD COLUMN human_request_id UUID;
