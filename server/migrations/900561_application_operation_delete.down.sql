ALTER TABLE application_operation DROP CONSTRAINT IF EXISTS application_operation_action_check;
ALTER TABLE application_operation ADD CONSTRAINT application_operation_action_check CHECK (action IN ('start', 'stop', 'restart', 'publish', 'unpublish'));
