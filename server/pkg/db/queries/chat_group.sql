-- Group chat: multiple human participants + a session agent, per project.

-- name: GetProjectDefaultGroup :one
SELECT * FROM chat_session
WHERE project_id = @project_id AND kind = 'group' AND is_project_default = true
LIMIT 1;

-- name: ListGroupParticipants :many
SELECT * FROM chat_participant WHERE chat_session_id = @chat_session_id ORDER BY joined_at;

-- name: AddGroupParticipant :one
INSERT INTO chat_participant (chat_session_id, member_type, member_id, joined_at)
VALUES (@chat_session_id, @member_type, @member_id, now())
ON CONFLICT (chat_session_id, member_type, member_id) DO NOTHING
RETURNING *;

-- name: RemoveGroupParticipant :exec
DELETE FROM chat_participant
WHERE chat_session_id = @chat_session_id AND member_type = @member_type AND member_id = @member_id;

-- name: IsGroupParticipant :one
SELECT EXISTS (SELECT 1 FROM chat_participant
    WHERE chat_session_id = @chat_session_id AND member_type = @member_type AND member_id = @member_id)::boolean;

-- name: ArchiveProjectDefaultGroup :exec
-- A project's default group archives only via project status (completed/cancelled);
-- it is never archived manually. Guarded on kind + is_project_default + active.
UPDATE chat_session SET status = 'archived', updated_at = now()
WHERE project_id = @project_id AND kind = 'group' AND is_project_default = true AND status = 'active';


-- name: CreateGroupChatSession :one
-- Creates a group chat session (kind='group'). Mirrors CreateChatSession but
-- sets kind='group'; is_project_default is a caller-supplied flag so the same
-- query serves both a project's default group (true) and a manually-created
-- standalone group (false, project_id may be null). runtime_id is resolved
-- from the agent like CreateChatSession.
INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, runtime_id, is_agent_intro, project_id, squad_id, kind, is_project_default, id)
VALUES ($1, $2, $3, $4, (SELECT runtime_id FROM agent WHERE id = $2), $5, sqlc.narg('project_id'), sqlc.narg('squad_id'), 'group', $6, COALESCE(sqlc.narg('id')::uuid, gen_random_uuid()))
RETURNING *;
