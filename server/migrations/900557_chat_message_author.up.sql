-- Message-level author. A 1:1 chat infers the human from chat_session.creator_id
-- (role='user'); a group chat has many humans, so each message records who sent
-- it. Nullable: legacy / 1:1 rows leave author_id null (resolved to creator_id).
ALTER TABLE chat_message ADD COLUMN author_type TEXT CHECK (author_type IN ('member', 'agent'));
ALTER TABLE chat_message ADD COLUMN author_id UUID;
