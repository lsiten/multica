-- name: GetAgentForTaskClaim :one
-- Reserve this agent's capacity without blocking admission for other agents.
SELECT * FROM agent WHERE id = $1 FOR UPDATE SKIP LOCKED;
