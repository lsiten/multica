-- name: GetRuntimeSupervisor :one
SELECT * FROM runtime_supervisor WHERE runtime_id=$1;

-- name: LockRuntimeSupervisor :one
SELECT * FROM runtime_supervisor WHERE runtime_id=$1 FOR UPDATE;

-- name: AcquireRuntimeSupervisor :one
INSERT INTO runtime_supervisor (runtime_id, workspace_id, daemon_id, instance_id, epoch)
SELECT @runtime_id, @workspace_id, @daemon_id, @instance_id, 1 WHERE @expected_epoch::bigint=0
ON CONFLICT (runtime_id) DO UPDATE SET instance_id=EXCLUDED.instance_id, epoch=runtime_supervisor.epoch+1
WHERE runtime_supervisor.epoch= @expected_epoch AND runtime_supervisor.workspace_id= @workspace_id AND runtime_supervisor.daemon_id= @daemon_id
RETURNING *;

-- name: TransferRuntimeSupervisor :one
UPDATE runtime_supervisor SET instance_id= @instance_id, epoch=epoch+1
WHERE runtime_id= @runtime_id AND workspace_id= @workspace_id AND daemon_id= @daemon_id AND epoch= @expected_epoch
RETURNING *;

-- name: LockTaskForExecution :one
SELECT * FROM agent_task_queue WHERE id=$1 FOR UPDATE;

-- name: GetTaskExecution :one
SELECT * FROM task_execution WHERE task_id=$1;

-- name: BindTaskExecution :one
INSERT INTO task_execution (task_id,execution_id,runtime_id,workspace_id,daemon_id,worker_id,dispatched_at,supervisor_epoch)
VALUES ( @task_id, @execution_id, @runtime_id, @workspace_id, @daemon_id, @worker_id, @dispatched_at, @supervisor_epoch)
ON CONFLICT(task_id) DO UPDATE SET execution_id=EXCLUDED.execution_id, runtime_id=EXCLUDED.runtime_id,
 workspace_id=EXCLUDED.workspace_id, daemon_id=EXCLUDED.daemon_id, worker_id=EXCLUDED.worker_id,
 dispatched_at=EXCLUDED.dispatched_at, supervisor_epoch=EXCLUDED.supervisor_epoch, revoked=false, created_at=now()
WHERE task_execution.dispatched_at<>EXCLUDED.dispatched_at OR task_execution.runtime_id<>EXCLUDED.runtime_id
RETURNING *;

-- name: AdoptTaskExecution :exec
UPDATE task_execution SET supervisor_epoch= @supervisor_epoch WHERE task_id= @task_id AND execution_id= @execution_id;

-- name: RevokeTaskExecution :exec
UPDATE task_execution SET revoked=true WHERE task_id= @task_id AND execution_id= @execution_id;

-- name: CreateExecutionGrant :one
INSERT INTO execution_grant(token_hash,task_id,execution_id,operations,expires_at)
VALUES ($1,$2,$3,$4,$5) RETURNING *;

-- name: GetExecutionGrant :one
SELECT g.*, e.workspace_id, e.runtime_id, e.daemon_id, e.worker_id FROM execution_grant g
JOIN task_execution e ON e.task_id=g.task_id AND e.execution_id=g.execution_id
JOIN agent_task_queue t ON t.id=e.task_id AND t.runtime_id=e.runtime_id AND t.dispatched_at=e.dispatched_at
JOIN agent_runtime r ON r.id=e.runtime_id AND r.workspace_id=e.workspace_id AND r.daemon_id=e.daemon_id
WHERE g.token_hash=$1 AND NOT g.revoked AND NOT e.revoked AND g.expires_at>now()
 AND (t.completed_at IS NULL OR t.completed_at>now()-interval '24 hours')
 AND EXISTS(SELECT 1 FROM member m WHERE m.workspace_id=e.workspace_id AND m.user_id=r.owner_id);

-- name: RevokeExecutionGrant :exec
UPDATE execution_grant SET revoked=true WHERE task_id= @task_id AND execution_id= @execution_id AND token_hash= @token_hash;

-- name: CreateExecutionSnapshot :exec
INSERT INTO execution_snapshot(runtime_id,snapshot_id,supervisor_epoch) VALUES($1,$2,$3)
ON CONFLICT(runtime_id,snapshot_id) DO NOTHING;

-- name: LockExecutionSnapshot :one
SELECT * FROM execution_snapshot WHERE runtime_id=$1 AND snapshot_id=$2 FOR UPDATE;

-- name: AppendExecutionSnapshot :exec
UPDATE execution_snapshot SET pages=pages || @page::jsonb, next_page=next_page+1, complete= @complete
WHERE runtime_id= @runtime_id AND snapshot_id= @snapshot_id;

-- name: RuntimeExecutionMembership :one
SELECT r.* FROM agent_runtime r JOIN member m ON m.user_id=r.owner_id AND m.workspace_id=r.workspace_id
WHERE r.id= @runtime_id AND r.workspace_id= @workspace_id AND r.daemon_id= @daemon_id
FOR SHARE OF m;

-- name: ListRuntimeExecutions :many
SELECT e.*, t.status FROM task_execution e JOIN agent_task_queue t ON t.id=e.task_id
WHERE e.runtime_id= @runtime_id AND e.workspace_id= @workspace_id AND e.daemon_id= @daemon_id
 AND t.runtime_id=e.runtime_id AND t.dispatched_at=e.dispatched_at
 AND (sqlc.narg(after_task_id)::uuid IS NULL OR e.task_id>sqlc.narg(after_task_id))
ORDER BY e.task_id LIMIT @page_limit;

-- name: LockExecutionChatSessions :many
SELECT chat_session.id FROM chat_session
WHERE chat_session.id IN (SELECT chat_session_id FROM agent_task_queue WHERE agent_task_queue.id=ANY(@task_ids::uuid[]))
ORDER BY chat_session.id FOR UPDATE;

-- name: GetExecutionMessageReceipt :one
SELECT payload_hash FROM execution_message_receipt WHERE execution_id= @execution_id AND sequence= @sequence;

-- name: CreateExecutionMessageReceipt :exec
INSERT INTO execution_message_receipt(execution_id,sequence,payload_hash) VALUES($1,$2,$3);
