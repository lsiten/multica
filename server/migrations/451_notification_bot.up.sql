CREATE TABLE notification_bot (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL,
    user_id uuid NOT NULL,
    name text NOT NULL,
    platform text NOT NULL CHECK (platform IN ('wecom', 'lark', 'dingtalk', 'slack', 'telegram')),
    credentials bytea NOT NULL,
    is_enabled boolean NOT NULL DEFAULT false,
    active_since timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_delivery_at timestamptz,
    last_error text NOT NULL DEFAULT ''
);
CREATE TABLE notification_bot_delivery (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    bot_id uuid NOT NULL,
    inbox_id uuid NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
