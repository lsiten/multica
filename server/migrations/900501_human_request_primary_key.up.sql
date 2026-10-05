DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'human_request'::regclass AND contype = 'p'
    ) THEN
        ALTER TABLE human_request ADD CONSTRAINT human_request_pkey
            PRIMARY KEY USING INDEX human_request_id_index;
    END IF;
END $$;
