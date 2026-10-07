ALTER TABLE application_operation
 DROP COLUMN IF EXISTS cancel_requested_at,
 DROP COLUMN IF EXISTS cancel_actor_type,
 DROP COLUMN IF EXISTS cancel_actor_id,
 DROP COLUMN IF EXISTS cancel_user_id,
 DROP COLUMN IF EXISTS cancel_task_id;
