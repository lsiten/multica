-- name: CreateTaskActorClaim :one
INSERT INTO task_actor_claim(token_hash,token_id,task_id,runtime_id,dispatched_at,agent_id,workspace_id,user_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING *;

-- name: GetTaskActorClaim :one
SELECT * FROM task_actor_claim WHERE token_hash=$1;

-- name: HasTaskActorClaim :one
SELECT EXISTS(SELECT 1 FROM task_actor_claim WHERE task_id=$1);

-- name: LockTaskActorToken :one
SELECT * FROM task_token WHERE token_hash=$1 AND expires_at>clock_timestamp()
FOR SHARE NOWAIT;

-- name: RetirePriorTaskActorTokens :exec
DELETE FROM task_token token
WHERE token.task_id= @task_id AND NOT EXISTS (
 SELECT 1 FROM task_actor_claim binding
 WHERE binding.token_hash=token.token_hash AND binding.token_id=token.id
 AND binding.task_id=token.task_id AND binding.runtime_id= @runtime_id
 AND binding.dispatched_at= @dispatched_at
 AND binding.agent_id= @agent_id AND binding.workspace_id= @workspace_id AND binding.user_id= @user_id
);

-- name: LockTaskActorPrincipals :one
SELECT r.* FROM agent_runtime r
JOIN agent a ON a.id= @agent_id AND a.runtime_id=r.id AND a.workspace_id=r.workspace_id
JOIN member m ON m.user_id= @user_id AND m.workspace_id=r.workspace_id
JOIN "user" actor ON actor.id=m.user_id
WHERE r.id= @runtime_id AND r.workspace_id= @workspace_id AND r.owner_id= @user_id
 AND a.archived_at IS NULL
FOR SHARE OF r,a,m,actor NOWAIT;

-- name: LockLegacyTaskActorMember :one
SELECT m.id FROM member m JOIN "user" actor ON actor.id=m.user_id
WHERE m.user_id= @user_id AND m.workspace_id= @workspace_id
FOR SHARE OF m,actor NOWAIT;

-- name: DeleteOrphanedTaskActorClaims :execrows
DELETE FROM task_actor_claim WHERE token_hash IN (
 SELECT binding.token_hash FROM task_actor_claim binding
 WHERE NOT EXISTS(SELECT 1 FROM agent_task_queue task WHERE task.id=binding.task_id)
 LIMIT 100
);
