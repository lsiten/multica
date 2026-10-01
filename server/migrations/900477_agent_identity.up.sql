CREATE TABLE agent_identity (
    agent_id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    email text,
    phone text,
    wallet_address text,
    budget_usd_ticks bigint NOT NULL DEFAULT 0 CHECK (budget_usd_ticks >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (email IS NULL OR char_length(email) <= 320),
    CHECK (phone IS NULL OR char_length(phone) <= 32),
    CHECK (wallet_address IS NULL OR char_length(wallet_address) <= 256)
);
