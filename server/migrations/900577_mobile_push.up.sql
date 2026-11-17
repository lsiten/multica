CREATE TABLE mobile_push_device (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL,
    token text NOT NULL,
    topic text NOT NULL,
    environment text NOT NULL CHECK (environment IN ('sandbox', 'production')),
    active_since timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mobile_push_delivery (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    device_id uuid NOT NULL,
    inbox_id uuid,
    chat_message_id uuid,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((inbox_id IS NOT NULL) <> (chat_message_id IS NOT NULL))
);
