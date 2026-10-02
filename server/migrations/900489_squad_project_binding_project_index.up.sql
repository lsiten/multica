CREATE INDEX CONCURRENTLY squad_project_binding_project_idx
    ON squad_project_binding (project_id, squad_id)
    WHERE active;
