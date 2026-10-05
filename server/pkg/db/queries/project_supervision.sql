-- name: GetProjectSupervision :one
SELECT * FROM project_supervision WHERE project_id=$1 AND workspace_id=$2;

-- name: LockProjectSupervision :one
SELECT * FROM project_supervision WHERE project_id=$1 AND workspace_id=$2 FOR UPDATE;

-- name: TryProjectSupervisionLock :one
SELECT pg_try_advisory_xact_lock(hashtextextended($1::text,98241))::boolean;

-- name: TryLockSupervisionAgent :one
SELECT * FROM agent WHERE id=$1 AND workspace_id=$2 FOR UPDATE SKIP LOCKED;

-- name: TryLockSupervisionWorkspace :one
SELECT id FROM workspace WHERE id=$1 FOR KEY SHARE SKIP LOCKED;

-- name: ManualProjectSupervisionCheck :exec
UPDATE project_supervision SET next_check_at=now(),dirty_version=dirty_version+1,no_progress_count=0 WHERE project_id=$1 AND workspace_id=$2 AND enabled;

-- name: ListDueProjectSupervisions :many
SELECT * FROM project_supervision
WHERE enabled AND next_check_at<=now()
ORDER BY last_coordination_at ASC NULLS FIRST,last_checked_at ASC NULLS FIRST,project_id LIMIT 100;

-- name: CreateProjectSupervision :one
INSERT INTO project_supervision(project_id,workspace_id,configured_by,config,enabled)
VALUES($1,$2,$3,$4,$5) ON CONFLICT(project_id) DO NOTHING RETURNING *;

-- name: SaveProjectSupervision :one
UPDATE project_supervision SET config=$3,enabled=$4,configured_by=$5,revision=revision+1,
 dirty_version=dirty_version+1,next_check_at=now(),updated_at=now(),no_progress_count=0
WHERE project_id=$1 AND workspace_id=$2 AND revision=$6 RETURNING *;

-- name: MarkProjectSupervisionDirty :exec
UPDATE project_supervision SET dirty_version=dirty_version+1,next_check_at=LEAST(next_check_at,now()),updated_at=now()
WHERE project_id=$1 AND workspace_id=$2 AND enabled;

-- name: MarkWorkspaceSupervisionDirty :exec
UPDATE project_supervision SET dirty_version=dirty_version+1,next_check_at=LEAST(next_check_at,now()),updated_at=now()
WHERE workspace_id=$1 AND enabled;

-- name: StoreProjectSupervisionCheck :one
UPDATE project_supervision SET last_checked_at=now(),next_check_at=$3,last_reason=$4,
 last_coordination_at=CASE WHEN $5::uuid IS NOT NULL AND last_task_id IS DISTINCT FROM $5 THEN now() ELSE last_coordination_at END,
 last_task_id=$5,last_fingerprint=$6,updated_at=now()
WHERE project_id=$1 AND workspace_id=$2 RETURNING *;

-- name: StoreProjectSupervisionResult :one
UPDATE project_supervision SET handled_version=GREATEST(handled_version,$3),last_result=$4,
 no_progress_count=$5,last_reason=$6,updated_at=now()
WHERE project_id=$1 AND workspace_id=$2 RETURNING *;

-- name: DeleteProjectSupervision :exec
DELETE FROM project_supervision WHERE project_id=$1 AND workspace_id=$2;

-- name: DeleteWorkspaceProjectSupervision :exec
DELETE FROM project_supervision WHERE workspace_id=$1;

-- name: ListSupervisionIssues :many
SELECT i.id,i.number,i.title,i.status,i.revision,i.assignee_type,i.assignee_id,i.parent_issue_id,i.stage,i.created_at,i.updated_at,COALESCE(s.category,'')::text AS status_category,
	COALESCE((SELECT latest.status FROM agent_task_queue latest WHERE latest.issue_id=i.id ORDER BY latest.created_at DESC,latest.id DESC LIMIT 1),'')::text AS last_run_status,
 (SELECT count(*) FROM agent_task_queue t WHERE t.issue_id=i.id
 AND t.status IN ('queued','deferred','dispatched','running','waiting_local_directory'))::int AS active_runs,
 EXISTS(SELECT 1 FROM issue_dependency d JOIN issue blocker ON blocker.id=CASE WHEN d.type='blocked_by' THEN d.depends_on_issue_id ELSE d.issue_id END AND blocker.workspace_id=i.workspace_id
 LEFT JOIN issue_status bs ON bs.workspace_id=blocker.workspace_id AND bs.key=blocker.status
 WHERE ((d.issue_id=i.id AND d.type='blocked_by') OR (d.depends_on_issue_id=i.id AND d.type='blocks'))
 AND (blocker.status='cancelled' OR (blocker.status<>'done' AND COALESCE(bs.category,'') NOT IN ('done','closed'))))::boolean AS dependency_blocked
FROM issue i LEFT JOIN issue_status s ON s.workspace_id=i.workspace_id AND s.key=i.status
WHERE i.workspace_id=$1 AND i.project_id=$2
ORDER BY CASE i.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END,i.created_at,i.id;

-- name: CountProjectExecutionRuns :one
SELECT count(*) FROM agent_task_queue t JOIN issue i ON i.id=t.issue_id
WHERE i.workspace_id=$1 AND i.project_id=$2 AND t.status=ANY(@statuses::text[])
AND (sqlc.narg(exclude_id)::uuid IS NULL OR t.id <> sqlc.narg(exclude_id)::uuid);

-- name: CountPendingCoordinationRuns :one
SELECT count(*) FROM agent_task_queue WHERE agent_id=$1
AND context->>'type'='project_supervision' AND status IN ('queued','deferred','dispatched','running','waiting_local_directory');

-- name: CountAgentReservedRuns :one
SELECT count(*) FROM agent_task_queue WHERE agent_id=$1
AND status IN ('queued','deferred','dispatched','running','waiting_local_directory');

-- name: SetSupervisionIssueAssignment :one
UPDATE issue SET assignee_type=$3,assignee_id=$4,revision=revision+1,updated_at=now()
WHERE id=$1 AND workspace_id=$2 AND project_id=$5 AND revision=$6 RETURNING *;

-- name: PromoteSupervisionIssue :one
UPDATE issue SET status='todo',revision=revision+1,updated_at=now()
WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND status='backlog' AND revision=$4 RETURNING *;

-- name: GetSupervisionIssueEvidence :one
SELECT (SELECT count(*) FROM agent_task_queue active WHERE active.issue_id=sqlc.arg(issue_id)::uuid AND active.status IN ('queued','deferred','dispatched','running','waiting_local_directory'))::int AS active_runs,
 EXISTS(SELECT 1 FROM issue candidate JOIN issue_dependency d ON (d.issue_id=candidate.id AND d.type='blocked_by') OR (d.depends_on_issue_id=candidate.id AND d.type='blocks')
 JOIN issue blocker ON blocker.id=CASE WHEN d.type='blocked_by' THEN d.depends_on_issue_id ELSE d.issue_id END AND blocker.workspace_id=candidate.workspace_id
 LEFT JOIN issue_status bs ON bs.workspace_id=blocker.workspace_id AND bs.key=blocker.status
 WHERE candidate.id=sqlc.arg(issue_id)::uuid AND (blocker.status='cancelled' OR (blocker.status<>'done' AND COALESCE(bs.category,'') NOT IN ('done','closed'))))::boolean AS dependency_blocked,
 EXISTS(SELECT 1 FROM agent_task_queue delivered WHERE delivered.id=(SELECT newest.id FROM agent_task_queue newest WHERE newest.issue_id=sqlc.arg(issue_id)::uuid ORDER BY newest.created_at DESC,newest.id DESC LIMIT 1) AND delivered.status='completed' AND COALESCE(delivered.result->>'summary','')<>'')::boolean AS has_delivery,
 COALESCE((SELECT latest.status FROM agent_task_queue latest WHERE latest.issue_id=sqlc.arg(issue_id)::uuid ORDER BY latest.created_at DESC,latest.id DESC LIMIT 1),'')::text AS last_run_status,
 COALESCE((SELECT latest.handoff_note FROM agent_task_queue latest WHERE latest.issue_id=sqlc.arg(issue_id)::uuid ORDER BY latest.created_at DESC,latest.id DESC LIMIT 1),'')::text AS last_handoff,
 (SELECT count(*) FROM agent_task_queue attempts WHERE attempts.issue_id=sqlc.arg(issue_id)::uuid AND attempts.status='failed' AND attempts.trigger_evidence_kind='project_supervision'
 AND attempts.created_at>COALESCE((SELECT max(success.created_at) FROM agent_task_queue success WHERE success.issue_id=sqlc.arg(issue_id)::uuid AND success.status='completed'),'-infinity'::timestamptz))::int AS failed_supervision_attempts;

-- name: GetSupervisionAgentRuntime :one
SELECT r.status,r.last_seen_at,r.updated_at,r.visibility,r.owner_id,r.metadata,r.daemon_id
FROM agent_runtime r WHERE r.id=$1 AND r.workspace_id=$2;

-- name: CountDaemonSupervisionReservations :one
SELECT count(*) FROM agent_task_queue t JOIN agent_runtime r ON r.id=t.runtime_id
JOIN agent_runtime target ON target.id=$1
WHERE t.status=ANY(@statuses::text[]) AND (sqlc.narg(exclude_id)::uuid IS NULL OR t.id <> sqlc.narg(exclude_id)::uuid)
AND ((target.daemon_id IS NULL AND r.id=target.id) OR (target.daemon_id IS NOT NULL AND r.daemon_id=target.daemon_id AND r.owner_id IS NOT DISTINCT FROM target.owner_id));

-- name: CancelQueuedProjectCoordination :many
UPDATE agent_task_queue SET status='cancelled',completed_at=now(),error='project supervision changed'
WHERE context->>'type'='project_supervision' AND context->>'project_id'=sqlc.arg(project_id)::text
AND status IN ('queued','deferred') RETURNING *;

-- name: CreateProjectCoordinationTask :one
INSERT INTO agent_task_queue(id,agent_id,runtime_id,status,priority,context,
 originator_user_id,accountable_user_id,originator_source,trigger_evidence_kind,trigger_evidence_ref_id,
 runtime_mcp_overlay,runtime_connected_apps,max_attempts)
SELECT $1,$2,$3,'queued',3,$4,$5,$5,'direct_human','project_supervision',$6,$7,$8,1
WHERE lock_task_owner_rows($2,NULL,$3) RETURNING *;

-- name: SetTaskProjectContext :one
UPDATE agent_task_queue SET context=COALESCE(context,'{}'::jsonb)||sqlc.arg(context_patch)::jsonb WHERE id=sqlc.arg(id) RETURNING *;

-- name: RequestProjectSupervisionCheck :exec
UPDATE project_supervision SET next_check_at=now() WHERE project_id=$1 AND workspace_id=$2 AND enabled;

-- name: RequestWorkspaceSupervisionCapacityChecks :exec
UPDATE project_supervision SET next_check_at=now() WHERE workspace_id=$1 AND enabled
AND last_reason IN ('lead_capacity','agent_capacity','runtime_capacity','capacity_wait');

-- name: MergeQueuedProjectCoordination :one
UPDATE agent_task_queue SET context=sqlc.arg(context)::jsonb || CASE WHEN context ? 'human_response' THEN jsonb_build_object('human_response', context->'human_response') ELSE '{}'::jsonb END WHERE id=$1 AND status='queued' AND dispatched_at IS NULL RETURNING *;

-- name: CancelObsoleteProjectCoordination :many
UPDATE agent_task_queue t SET status='cancelled',completed_at=now(),error='project lead changed'
FROM project p WHERE p.id=$1 AND p.workspace_id=$2 AND t.context->>'project_id'=p.id::text AND t.context->>'type'='project_supervision'
AND (p.status<>'in_progress' OR p.lead_type IS DISTINCT FROM 'agent' OR p.lead_id IS DISTINCT FROM t.agent_id)
AND t.status IN ('queued','deferred') RETURNING t.*;
