CREATE INDEX CONCURRENTLY agent_project_binding_project_idx
    ON agent_project_binding (project_id, agent_id)
    WHERE active;
