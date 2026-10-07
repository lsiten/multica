CREATE INDEX CONCURRENTLY IF NOT EXISTS application_operation_deadline_index ON application_operation(deadline_at,id) WHERE state IN ('queued','running','cancelling');
