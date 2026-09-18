CREATE INDEX CONCURRENTLY runtime_vscreen_intervention_unresolved_idx ON runtime_vscreen_intervention (workspace_id, runtime_id) WHERE state IN ('awaiting_takeover','human','ready_to_continue');
