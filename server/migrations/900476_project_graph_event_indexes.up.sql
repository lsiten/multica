CREATE INDEX CONCURRENTLY project_graph_event_project_created_idx
    ON project_graph_event (project_id, created_at, id);
