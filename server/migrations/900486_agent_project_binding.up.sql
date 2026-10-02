-- Optional project affinity for agents. An agent with no active rows remains
-- workspace-scoped for backwards compatibility; once rows exist, only the
-- listed projects are valid execution contexts.
CREATE TABLE agent_project_binding (
    agent_id UUID NOT NULL,
    project_id UUID NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, project_id)
);
