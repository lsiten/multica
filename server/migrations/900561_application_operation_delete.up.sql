-- The "delete" operation reuses the stop release path: the daemon receives a
-- stop command and the server removes the confirmed-stopped instance on
-- complete. Allow the action so a delete operation can be enqueued.
ALTER TABLE application_operation DROP CONSTRAINT IF EXISTS application_operation_action_check;
ALTER TABLE application_operation ADD CONSTRAINT application_operation_action_check CHECK (action IN ('start', 'stop', 'restart', 'publish', 'unpublish', 'delete'));
