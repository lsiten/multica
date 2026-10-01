CREATE INDEX CONCURRENTLY project_graph_event_workspace_created_idx
    ON project_graph_event (workspace_id, created_at, id);
