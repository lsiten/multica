CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS squad_collaboration_history_revision_idx ON squad_collaboration_history (squad_id, revision);
