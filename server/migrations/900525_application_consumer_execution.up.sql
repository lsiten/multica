ALTER TABLE application_instance_consumer
 ADD COLUMN wave integer NOT NULL DEFAULT 0,
 ADD COLUMN owns_lifecycle boolean NOT NULL DEFAULT true,
 ADD COLUMN created_operation_id uuid;
