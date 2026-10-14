-- Group chat. A chat session may host multiple human participants plus an agent
-- (kind='group'); kind='direct' is the existing 1:1 shape (creator + agent).
-- `is_project_default` marks a project's auto-created default group (created when
-- the project gets a lead). No manual archive for group sessions: they archive
-- only via project status (completed/cancelled) — enforced in the app layer.
ALTER TABLE chat_session ADD COLUMN kind TEXT NOT NULL DEFAULT 'direct'
    CHECK (kind IN ('direct', 'group'));
ALTER TABLE chat_session ADD COLUMN is_project_default BOOLEAN NOT NULL DEFAULT false;
