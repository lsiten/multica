-- Optional project affinity for squads. Existing squads without rows remain
-- workspace-scoped until an owner explicitly configures project bindings.
CREATE TABLE squad_project_binding (
    squad_id UUID NOT NULL,
    project_id UUID NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (squad_id, project_id)
);
