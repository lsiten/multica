ALTER TABLE application_instance_consumer
 DROP COLUMN IF EXISTS created_operation_id,
 DROP COLUMN IF EXISTS owns_lifecycle,
 DROP COLUMN IF EXISTS wave;
