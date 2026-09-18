-- name: CreateVscreenIntervention :one
INSERT INTO runtime_vscreen_intervention (id, workspace_id, runtime_id, agent_id, source_task_id, reason, state, native_epoch, display_generation, geometry_revision, last_action_id, created_by_user_id)
VALUES ($1,$2,$3,$4,$5,$6,'awaiting_takeover',$7,$8,$9,$10,$11)
ON CONFLICT (id) DO NOTHING RETURNING *;

-- name: GetVscreenIntervention :one
SELECT * FROM runtime_vscreen_intervention WHERE id=$1 AND workspace_id=$2;

-- name: LockVscreenIntervention :one
SELECT * FROM runtime_vscreen_intervention WHERE id=$1 AND workspace_id=$2 FOR UPDATE;

-- name: ListVscreenInterventions :many
SELECT * FROM runtime_vscreen_intervention WHERE workspace_id=$1 AND runtime_id=$2 ORDER BY created_at DESC LIMIT 100;

-- name: UpdateVscreenIntervention :one
UPDATE runtime_vscreen_intervention SET state=$3, return_receipt_id=$4, last_action_id=$5, geometry_revision=$6, updated_at=now(), version=version+1,
 resolved_at=CASE WHEN $3 IN ('stale','cancelled') THEN now() ELSE NULL END
WHERE id=$1 AND workspace_id=$2 RETURNING *;

-- name: ConsumeVscreenIntervention :one
UPDATE runtime_vscreen_intervention SET state='continued', continuation_task_id=$3, human_summary=$4, resolved_at=now(),updated_at=now(),version=version+1
WHERE id=$1 AND workspace_id=$2 AND state='ready_to_continue' AND version=$5 RETURNING *;

-- name: LockVscreenSourceTask :one
SELECT * FROM agent_task_queue WHERE id=$1 FOR UPDATE;

-- name: LockVscreenWorkspace :one
SELECT id FROM workspace WHERE id=$1 FOR KEY SHARE;

-- name: LockVscreenInvocationTargets :many
SELECT * FROM agent_invocation_target WHERE agent_id=$1 FOR SHARE;

-- name: SetVscreenContinuationContext :one
UPDATE agent_task_queue SET rerun_of_task_id=$2, handoff_note=$4::text, context=COALESCE(context,'{}'::jsonb) || jsonb_build_object('vscreen_intervention', jsonb_build_object('intervention_id',$3::text,'human_summary',$4::text,'fresh_session',$5::boolean)),
 chat_input_task_id=CASE WHEN chat_session_id IS NOT NULL THEN $6 ELSE chat_input_task_id END
WHERE id=$1 RETURNING *;

-- name: CancelVscreenInterventionsByRuntime :exec
UPDATE runtime_vscreen_intervention SET state='cancelled',resolved_at=now(),updated_at=now(),version=version+1
WHERE runtime_id=$1 AND state IN ('awaiting_takeover','human','ready_to_continue');

-- name: DeleteVscreenInterventionsByWorkspace :exec
DELETE FROM runtime_vscreen_intervention WHERE workspace_id=$1;

-- name: CancelVscreenInterventionsByAgent :exec
UPDATE runtime_vscreen_intervention SET state='cancelled',resolved_at=now(),updated_at=now(),version=version+1
WHERE agent_id=$1 AND state IN ('awaiting_takeover','human','ready_to_continue');

-- name: GetVscreenInterventionByContinuation :one
SELECT * FROM runtime_vscreen_intervention WHERE continuation_task_id=$1 AND workspace_id=$2;
