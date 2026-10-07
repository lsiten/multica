ALTER TABLE application_access_ticket ADD COLUMN IF NOT EXISTS member_id uuid;

-- Existing tickets cannot prove their original membership; fail closed.
DELETE FROM application_access_ticket WHERE member_id IS NULL;

ALTER TABLE application_access_ticket ALTER COLUMN member_id SET NOT NULL;
