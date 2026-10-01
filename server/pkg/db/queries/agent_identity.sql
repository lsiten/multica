-- name: GetAgentIdentity :one
SELECT *
FROM agent_identity
WHERE agent_id = $1 AND workspace_id = $2;

-- name: UpsertAgentIdentity :one
INSERT INTO agent_identity (
    agent_id, workspace_id, email, phone, wallet_address, budget_usd_ticks
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (agent_id) DO UPDATE SET
    workspace_id = EXCLUDED.workspace_id,
    email = EXCLUDED.email,
    phone = EXCLUDED.phone,
    wallet_address = EXCLUDED.wallet_address,
    budget_usd_ticks = EXCLUDED.budget_usd_ticks,
    updated_at = now()
RETURNING *;
