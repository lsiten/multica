ALTER TABLE application_operation
 ADD COLUMN cancel_requested_at timestamptz,
 ADD COLUMN cancel_actor_type text,
 ADD COLUMN cancel_actor_id uuid,
 ADD COLUMN cancel_user_id uuid,
 ADD COLUMN cancel_task_id uuid;
