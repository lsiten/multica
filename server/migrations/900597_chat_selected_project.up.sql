ALTER TABLE chat_session
    ADD COLUMN IF NOT EXISTS selected_project_id UUID;

UPDATE chat_session
SET selected_project_id = project_id
WHERE selected_project_id IS NULL
  AND project_id IS NOT NULL;
