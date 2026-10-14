-- Group-chat human participants. A 1:1 chat has no rows (its single human is
-- chat_session.creator_id); a group chat lists its members here so several
-- humans + the session agent share one session.
-- No FK / cascade (DB rules): integrity enforced in the app layer.
CREATE TABLE chat_participant (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_session_id UUID NOT NULL,
    member_type     TEXT NOT NULL CHECK (member_type IN ('member', 'agent')),
    member_id       UUID NOT NULL,
    joined_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
