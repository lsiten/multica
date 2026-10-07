-- name: ListApplications :many
SELECT a.*,r.config,r.relations AS revision_relations
FROM application a JOIN application_revision r ON r.application_id=a.id AND r.workspace_id=a.workspace_id AND r.revision=a.revision
WHERE a.workspace_id=$1 AND (sqlc.narg(project_id)::uuid IS NULL OR a.project_id=sqlc.narg(project_id)::uuid)
ORDER BY a.created_at,a.id;

-- name: GetApplication :one
SELECT a.*,r.config,r.relations AS revision_relations
FROM application a JOIN application_revision r ON r.application_id=a.id AND r.workspace_id=a.workspace_id AND r.revision=a.revision
WHERE a.id=$1 AND a.workspace_id=$2;

-- name: GetApplicationRevision :one
SELECT * FROM application_revision WHERE application_id=$1 AND workspace_id=$2 AND revision=$3;

-- name: LockApplicationProject :one
SELECT id FROM project WHERE id=$1 AND workspace_id=$2 FOR UPDATE;

-- name: LockApplicationWorkspace :one
SELECT id FROM workspace WHERE id=$1 FOR KEY SHARE;

-- name: LockApplicationRuntime :one
SELECT * FROM agent_runtime WHERE id=$1 AND workspace_id=$2 FOR KEY SHARE;

-- name: CreateApplication :one
INSERT INTO application(workspace_id,project_id,name,description,kind,created_by)
VALUES($1,$2,$3,$4,$5,$6) RETURNING *;

-- name: InsertApplicationRevision :one
INSERT INTO application_revision(application_id,workspace_id,revision,config,relations,actor_type,actor_id)
VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING *;

-- name: UpdateApplication :one
UPDATE application SET name=$3,description=$4,revision=revision+1,updated_at=now()
WHERE id=$1 AND workspace_id=$2 AND revision=$5 RETURNING *;

-- name: DeleteApplication :exec
DELETE FROM application WHERE id=$1 AND workspace_id=$2;

-- name: DeleteApplicationRevisions :exec
DELETE FROM application_revision WHERE application_id=$1 AND workspace_id=$2;

-- name: ListApplicationRelations :many
SELECT * FROM application_relation WHERE workspace_id=$1 AND project_id=$2 ORDER BY source_id,type,target_id;

-- name: ReplaceApplicationRelations :exec
DELETE FROM application_relation WHERE source_id=$1 AND workspace_id=$2;

-- name: DeleteApplicationRelations :exec
DELETE FROM application_relation WHERE workspace_id=$1 AND (source_id=$2 OR target_id=$2);

-- name: InsertApplicationRelation :one
INSERT INTO application_relation(workspace_id,project_id,source_id,target_id,type,required,condition,start_external)
VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *;

-- name: CountApplicationDependents :one
SELECT count(*) FROM application_relation WHERE workspace_id=$1 AND target_id=$2;

-- name: ListApplicationInstances :many
SELECT i.*,r.status AS runtime_status,r.last_seen_at AS runtime_last_seen_at
FROM application_instance i LEFT JOIN agent_runtime r ON r.id=i.runtime_id AND r.workspace_id=i.workspace_id
WHERE i.workspace_id=$1 AND (sqlc.narg(application_id)::uuid IS NULL OR i.application_id=sqlc.narg(application_id)::uuid)
ORDER BY i.created_at,i.id;

-- name: GetApplicationInstance :one
SELECT * FROM application_instance WHERE id=$1 AND workspace_id=$2;

-- name: GetApplicationInstanceByRuntime :one
SELECT * FROM application_instance WHERE application_id=$1 AND runtime_id=$2 AND workspace_id=$3 FOR UPDATE;

-- name: UpsertApplicationInstance :one
INSERT INTO application_instance(workspace_id,application_id,runtime_id,daemon_id,revision)
VALUES($1,$2,$3,$4,$5)
ON CONFLICT(application_id,runtime_id) DO UPDATE SET revision=EXCLUDED.revision,daemon_id=EXCLUDED.daemon_id,updated_at=now()
RETURNING *;

-- name: SetApplicationInstanceDesiredState :one
UPDATE application_instance SET desired_state=$3,generation=generation+1,updated_at=now()
WHERE id=$1 AND workspace_id=$2 RETURNING *;

-- name: ReportApplicationInstance :one
WITH workspace_lock AS MATERIALIZED (
 SELECT id FROM workspace WHERE id=$2 FOR KEY SHARE
), project_lock AS MATERIALIZED (
 SELECT p.id FROM project p JOIN application a ON a.project_id=p.id AND a.workspace_id=p.workspace_id
 JOIN application_instance i ON i.application_id=a.id AND i.workspace_id=a.workspace_id
 WHERE i.id=$1 AND i.workspace_id=$2 AND EXISTS(SELECT 1 FROM workspace_lock) FOR KEY SHARE OF p
), runtime_lock AS MATERIALIZED (
 SELECT r.id FROM agent_runtime r JOIN application_instance i ON i.runtime_id=r.id AND i.workspace_id=r.workspace_id
 WHERE i.id=$1 AND i.workspace_id=$2 AND EXISTS(SELECT 1 FROM project_lock) FOR KEY SHARE OF r
)
UPDATE application_instance AS target SET observed_generation=$3,observed_revision=$4,process_state=$5,health_state=$6,error=$7,code_version=$8,dirty=$9,
started_at=$10,observed_at=now(),metrics=$11,updated_at=now()
WHERE target.id=$1 AND target.workspace_id=$2 AND target.generation=$3 AND EXISTS(SELECT 1 FROM runtime_lock) RETURNING target.*;

-- name: DeleteApplicationInstances :exec
DELETE FROM application_instance WHERE application_id=$1 AND workspace_id=$2;

-- name: ListApplicationEndpoints :many
SELECT * FROM application_endpoint WHERE workspace_id=$1 AND (sqlc.narg(application_id)::uuid IS NULL OR application_id=sqlc.narg(application_id)::uuid)
ORDER BY created_at,id;

-- name: GetApplicationEndpoint :one
SELECT * FROM application_endpoint WHERE id=$1 AND workspace_id=$2;

-- name: GetPublishedApplicationEndpoint :one
SELECT * FROM application_endpoint WHERE id=$1 AND state='published';

-- name: UpsertApplicationEndpoint :one
INSERT INTO application_endpoint(workspace_id,application_id,instance_id,port,entry_path,visibility,published_by,state)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT(instance_id) DO UPDATE SET port=EXCLUDED.port,entry_path=EXCLUDED.entry_path,visibility=EXCLUDED.visibility,published_by=EXCLUDED.published_by,state=EXCLUDED.state,revision=application_endpoint.revision+1,updated_at=now()
RETURNING *;

-- name: SetApplicationEndpointState :exec
UPDATE application_endpoint SET state=$3,revision=revision+1,updated_at=now() WHERE instance_id=$1 AND workspace_id=$2;

-- name: DeleteApplicationEndpoints :exec
DELETE FROM application_endpoint WHERE application_id=$1 AND workspace_id=$2;

-- name: CreateApplicationOperation :one
INSERT INTO application_operation(workspace_id,application_id,action,actor_type,actor_id,user_id,task_id,idempotency_key,request_hash,plan)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING *;

-- name: GetApplicationOperation :one
SELECT * FROM application_operation WHERE id=$1 AND workspace_id=$2;

-- name: GetApplicationOperationByKey :one
SELECT * FROM application_operation WHERE workspace_id=$1 AND user_id=$2 AND idempotency_key=$3;

-- name: ListApplicationOperations :many
SELECT * FROM application_operation WHERE workspace_id=$1 AND (sqlc.narg(application_id)::uuid IS NULL OR application_id=sqlc.narg(application_id)::uuid)
ORDER BY created_at DESC,id DESC LIMIT 100;

-- name: SetApplicationOperationState :exec
UPDATE application_operation SET state=$3,error=$4,updated_at=now(),completed_at=CASE WHEN $3::text IN ('completed','partial','failed','cancelled') THEN now() ELSE NULL END
WHERE id=$1 AND workspace_id=$2;

-- name: SetApplicationOperationDeadline :exec
UPDATE application_operation SET deadline_at=$3 WHERE id=$1 AND workspace_id=$2;

-- name: ListExpiredApplicationOperations :many
SELECT * FROM application_operation WHERE state IN ('queued','running','cancelling')
 AND deadline_at<=sqlc.arg(expired_at) ORDER BY deadline_at,id LIMIT sqlc.arg(batch_size);

-- name: FailPendingApplicationOperationSteps :exec
UPDATE application_operation_step SET state='failed',error=$3,completed_at=now(),claim_token=NULL,lease_until=NULL
WHERE operation_id=$1 AND workspace_id=$2 AND state IN ('queued','running');

-- name: InsertApplicationOperationStep :one
INSERT INTO application_operation_step(workspace_id,operation_id,instance_id,application_id,runtime_id,generation,wave,required,action,command)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING *;

-- name: ListApplicationOperationSteps :many
SELECT * FROM application_operation_step WHERE operation_id=$1 AND workspace_id=$2 ORDER BY wave,application_id;

-- name: CountApplicationInstanceOperations :one
SELECT count(*) FROM application_operation_step WHERE instance_id=$1 AND workspace_id=$2 AND state IN ('queued','running');

-- name: ListRuntimeApplicationInstances :many
SELECT * FROM application_instance WHERE workspace_id=$1 AND runtime_id=$2;

-- name: ClaimApplicationOperationSteps :many
UPDATE application_operation_step s SET state='running',claim_token=gen_random_uuid(),claimed_at=COALESCE(claimed_at,now()),lease_until=now()+interval '45 seconds'
WHERE s.id IN (
 SELECT candidate.id FROM application_operation_step candidate
 JOIN application_operation o ON o.id=candidate.operation_id AND o.workspace_id=candidate.workspace_id
 JOIN application a ON a.id=candidate.application_id AND a.workspace_id=candidate.workspace_id
 WHERE candidate.workspace_id=$1 AND candidate.runtime_id=$2 AND a.project_id=ANY(sqlc.arg(project_ids)::uuid[]) AND o.state IN ('queued','running','cancelling') AND o.deadline_at>now()
 AND ((candidate.state='queued' AND (candidate.lease_until IS NULL OR candidate.lease_until<now())) OR (candidate.state='running' AND candidate.lease_until<now()))
 AND NOT EXISTS(SELECT 1 FROM application_operation_step previous WHERE previous.operation_id=candidate.operation_id
 AND previous.workspace_id=candidate.workspace_id AND previous.wave<candidate.wave AND previous.state IN ('queued','running'))
 ORDER BY candidate.wave,candidate.id FOR UPDATE OF candidate SKIP LOCKED LIMIT 16
) RETURNING s.*;

-- name: ExtendApplicationOperationStepLease :execrows
UPDATE application_operation_step step SET lease_until=now()+interval '45 seconds'
WHERE step.id=$1 AND step.workspace_id=$2 AND step.runtime_id=$3 AND step.claim_token=$4 AND step.state='running'
 AND EXISTS(SELECT 1 FROM application_operation operation WHERE operation.id=step.operation_id AND operation.workspace_id=step.workspace_id AND operation.deadline_at>now());

-- name: CompleteApplicationOperationStep :one
UPDATE application_operation_step step SET state=$5,error=$6,completed_at=now(),lease_until=NULL
WHERE step.id=$1 AND step.workspace_id=$2 AND step.runtime_id=$3 AND step.claim_token=$4 AND step.state='running'
 AND EXISTS(SELECT 1 FROM application_operation operation WHERE operation.id=step.operation_id AND operation.workspace_id=step.workspace_id AND operation.deadline_at>now()) RETURNING step.*;

-- name: AddApplicationInstanceConsumer :one
INSERT INTO application_instance_consumer(workspace_id,instance_id,root_application_id,root_runtime_id,actor_id,wave,created_operation_id,owns_lifecycle)
VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(instance_id,root_application_id,root_runtime_id) DO UPDATE SET wave=GREATEST(application_instance_consumer.wave,EXCLUDED.wave),owns_lifecycle=application_instance_consumer.owns_lifecycle OR EXCLUDED.owns_lifecycle
RETURNING *, (xmax=0)::boolean AS inserted;

-- name: ReleaseApplicationInstanceConsumer :exec
DELETE FROM application_instance_consumer WHERE workspace_id=$1 AND root_application_id=$2 AND root_runtime_id=$3;

-- name: ListApplicationInstanceConsumers :many
SELECT * FROM application_instance_consumer WHERE workspace_id=$1 AND instance_id=$2;

-- name: ListApplicationRootInstances :many
SELECT i.* FROM application_instance i JOIN application_instance_consumer c ON c.instance_id=i.id AND c.workspace_id=i.workspace_id
WHERE c.workspace_id=$1 AND c.root_application_id=$2 AND c.root_runtime_id=$3 ORDER BY c.wave DESC,i.id;

-- name: CountApplicationActiveInstances :one
SELECT count(*) FROM application_instance i WHERE i.workspace_id=$1
AND (i.application_id=$2 OR EXISTS(SELECT 1 FROM application_instance_consumer c WHERE c.workspace_id=i.workspace_id AND c.instance_id=i.id AND c.root_application_id=$2))
AND (i.desired_state='running' OR i.process_state NOT IN ('stopped','failed') OR i.generation<>i.observed_generation
 OR EXISTS(SELECT 1 FROM application_operation_step s WHERE s.workspace_id=i.workspace_id AND s.instance_id=i.id AND s.state IN ('queued','running')));

-- name: DeleteApplicationInstanceConsumers :exec
DELETE FROM application_instance_consumer c WHERE c.workspace_id=$1 AND (c.root_application_id=$2 OR c.instance_id IN (SELECT i.id FROM application_instance i WHERE i.application_id=$2 AND i.workspace_id=$1));

-- name: ReleaseApplicationConsumer :exec
DELETE FROM application_instance_consumer WHERE workspace_id=$1 AND instance_id=$2 AND root_application_id=$3 AND root_runtime_id=$4;

-- name: ReleaseNewApplicationConsumer :exec
DELETE FROM application_instance_consumer WHERE workspace_id=$1 AND instance_id=$2 AND created_operation_id=$3;

-- name: GetApplicationOperationStep :one
SELECT * FROM application_operation_step WHERE id=$1 AND workspace_id=$2 FOR UPDATE;

-- name: LockApplicationOperation :one
SELECT * FROM application_operation WHERE id=$1 AND workspace_id=$2 FOR UPDATE;

-- name: ReadApplicationOperationStep :one
SELECT * FROM application_operation_step WHERE id=$1 AND workspace_id=$2;

-- name: LockRuntimeApplicationProjects :many
SELECT p.id FROM project p WHERE p.workspace_id=$1 AND EXISTS(
 SELECT 1 FROM application_operation_step s JOIN application a ON a.id=s.application_id AND a.workspace_id=s.workspace_id
 JOIN application_operation o ON o.id=s.operation_id AND o.workspace_id=s.workspace_id
 WHERE s.workspace_id=p.workspace_id AND a.project_id=p.id AND s.runtime_id=$2
 AND s.state IN ('queued','running') AND o.state IN ('queued','running','cancelling'))
ORDER BY p.id FOR UPDATE;

-- name: CancelApplicationOperationSteps :exec
UPDATE application_operation_step SET state='cancelled',completed_at=now(),claim_token=NULL,lease_until=NULL,error='operation cancelled'
WHERE operation_id=$1 AND workspace_id=$2 AND state IN ('queued','running');

-- name: RequestApplicationOperationCancellation :one
UPDATE application_operation SET cancel_requested_at=now(),cancel_actor_type=$3,cancel_actor_id=$4,cancel_user_id=$5,cancel_task_id=$6,state='cancelling',updated_at=now(),deadline_at=now()+interval '15 minutes'
WHERE id=$1 AND workspace_id=$2 AND cancel_requested_at IS NULL RETURNING *;

-- name: ConfirmUnstartedApplicationCancellation :one
UPDATE application_instance SET observed_generation=generation,observed_revision=revision,process_state='stopped',health_state='unknown',error='',observed_at=now()
WHERE id=$1 AND workspace_id=$2 AND desired_state='stopped' AND generation=$3 RETURNING *;

-- name: FinishUnclaimedApplicationStep :one
UPDATE application_operation_step SET state=$3,error=$4,completed_at=now(),lease_until=NULL
WHERE id=$1 AND workspace_id=$2 AND state='queued' RETURNING *;

-- name: GetApplicationInstanceDependencies :many
SELECT * FROM application_instance WHERE workspace_id=$1 AND id=ANY(@ids::uuid[]);

-- name: DeferApplicationOperationStep :exec
UPDATE application_operation_step SET state='queued',claim_token=NULL,lease_until=now()+interval '3 seconds'
WHERE id=$1 AND workspace_id=$2 AND claim_token=$3 AND state='running';

-- name: GetApplicationInstanceStartCommand :one
SELECT s.command FROM application_operation_step s JOIN application_operation o ON o.id=s.operation_id AND o.workspace_id=s.workspace_id
WHERE s.instance_id=$1 AND s.workspace_id=$2 AND s.action IN ('start','restart','resume')
ORDER BY o.created_at DESC,s.id DESC LIMIT 1;

-- name: ApplicationInstanceMayRestore :one
SELECT EXISTS(SELECT 1 FROM application_operation_step s
 WHERE s.workspace_id=$1 AND s.instance_id=$2 AND s.generation=$3 AND s.action IN ('start','restart','resume') AND s.state='completed')
 AND NOT EXISTS(SELECT 1 FROM application_operation_step s
 WHERE s.workspace_id=$1 AND s.instance_id=$2 AND s.state IN ('queued','running')) AS may_restore;

-- name: CreateApplicationAccessTicket :exec
INSERT INTO application_access_ticket(token_hash,endpoint_id,workspace_id,user_id,endpoint_revision,member_id)
VALUES($1,$2,$3,$4,$5,$6);

-- name: ConsumeApplicationAccessTicket :one
DELETE FROM application_access_ticket WHERE token_hash=$1 AND endpoint_id=$2 AND expires_at>now() RETURNING *;

-- name: PruneApplicationAccessTickets :execrows
WITH expired AS MATERIALIZED (
 SELECT token_hash FROM application_access_ticket WHERE expires_at<now()
 ORDER BY expires_at,token_hash LIMIT sqlc.arg(batch_size)
 FOR UPDATE SKIP LOCKED
)
DELETE FROM application_access_ticket ticket USING expired
WHERE ticket.token_hash=expired.token_hash;

-- name: ListApplicationDeletionBlockers :many
SELECT a.id,a.name,count(*) OVER() AS total
FROM application a
WHERE a.workspace_id=sqlc.arg(workspace_id)::uuid
 AND (sqlc.narg(project_id)::uuid IS NULL OR a.project_id=sqlc.narg(project_id)::uuid)
 AND (
  EXISTS(SELECT 1 FROM application_instance i WHERE i.workspace_id=a.workspace_id
   AND (i.application_id=a.id OR EXISTS(SELECT 1 FROM application_instance_consumer c
     WHERE c.workspace_id=i.workspace_id AND c.instance_id=i.id AND c.root_application_id=a.id
       AND (sqlc.narg(runtime_id)::uuid IS NULL OR i.runtime_id=sqlc.narg(runtime_id)::uuid OR c.root_runtime_id=sqlc.narg(runtime_id)::uuid)))
   AND (sqlc.narg(runtime_id)::uuid IS NULL OR i.runtime_id=sqlc.narg(runtime_id)::uuid
     OR EXISTS(SELECT 1 FROM application_instance_consumer c WHERE c.workspace_id=i.workspace_id AND c.instance_id=i.id AND c.root_application_id=a.id AND c.root_runtime_id=sqlc.narg(runtime_id)::uuid))
   AND (i.desired_state='running' OR i.process_state NOT IN ('stopped','failed') OR i.generation<>i.observed_generation))
  OR EXISTS(SELECT 1 FROM application_operation o WHERE o.workspace_id=a.workspace_id AND o.application_id=a.id
   AND o.state IN ('queued','running','cancelling') AND (sqlc.narg(runtime_id)::uuid IS NULL OR o.plan->>'root_runtime_id'=sqlc.narg(runtime_id)::text
     OR EXISTS(SELECT 1 FROM application_operation_step s WHERE s.workspace_id=o.workspace_id AND s.operation_id=o.id AND s.runtime_id=sqlc.narg(runtime_id)::uuid AND s.state IN ('queued','running'))))
 )
ORDER BY a.id LIMIT 10;

-- name: ApplicationResourceInUse :one
SELECT EXISTS(
 SELECT 1 FROM application a JOIN application_revision r ON r.application_id=a.id AND r.workspace_id=a.workspace_id AND r.revision=a.revision
 WHERE a.workspace_id=sqlc.arg(workspace_id)::uuid AND a.project_id=sqlc.arg(project_id)::uuid AND r.config->>'resource_id'=sqlc.arg(resource_id)::text
 UNION ALL
 SELECT 1 FROM application_instance i JOIN application a ON a.id=i.application_id AND a.workspace_id=i.workspace_id
 JOIN application_revision r ON r.application_id=i.application_id AND r.workspace_id=i.workspace_id AND r.revision=i.revision
 WHERE i.workspace_id=sqlc.arg(workspace_id)::uuid AND a.project_id=sqlc.arg(project_id)::uuid AND r.config->>'resource_id'=sqlc.arg(resource_id)::text
 AND (i.desired_state='running' OR i.process_state NOT IN ('stopped','failed') OR i.generation<>i.observed_generation
   OR EXISTS(SELECT 1 FROM application_operation_step s WHERE s.workspace_id=i.workspace_id AND s.instance_id=i.id AND s.state IN ('queued','running')))
) AS in_use;

-- name: DeleteScopedApplicationTickets :exec
DELETE FROM application_access_ticket t WHERE t.workspace_id=sqlc.arg(workspace_id)::uuid AND (sqlc.narg(project_id)::uuid IS NULL OR t.endpoint_id IN (
 SELECT e.id FROM application_endpoint e JOIN application a ON a.id=e.application_id AND a.workspace_id=e.workspace_id
 WHERE a.workspace_id=sqlc.arg(workspace_id)::uuid AND a.project_id=sqlc.narg(project_id)::uuid));

-- name: ListScopedApplicationEndpointIDs :many
SELECT e.id FROM application_endpoint e JOIN application a ON a.id=e.application_id AND a.workspace_id=e.workspace_id
WHERE a.workspace_id=$1 AND (sqlc.narg(project_id)::uuid IS NULL OR a.project_id=sqlc.narg(project_id)::uuid);

-- name: DeleteScopedApplicationConsumers :exec
DELETE FROM application_instance_consumer c WHERE c.workspace_id=sqlc.arg(workspace_id)::uuid AND (sqlc.narg(project_id)::uuid IS NULL OR
 c.root_application_id IN(SELECT a.id FROM application a WHERE a.workspace_id=sqlc.arg(workspace_id)::uuid AND (sqlc.narg(project_id)::uuid IS NULL OR a.project_id=sqlc.narg(project_id)::uuid))
 OR c.instance_id IN(SELECT i.id FROM application_instance i JOIN application a ON a.id=i.application_id AND a.workspace_id=i.workspace_id WHERE a.workspace_id=sqlc.arg(workspace_id)::uuid AND (sqlc.narg(project_id)::uuid IS NULL OR a.project_id=sqlc.narg(project_id)::uuid)));

-- name: DeleteScopedApplicationEndpoints :exec
DELETE FROM application_endpoint e WHERE e.workspace_id=sqlc.arg(workspace_id)::uuid AND (sqlc.narg(project_id)::uuid IS NULL OR e.application_id IN (
 SELECT a.id FROM application a WHERE a.workspace_id=sqlc.arg(workspace_id)::uuid AND a.project_id=sqlc.narg(project_id)::uuid));

-- name: DeleteScopedApplicationSteps :exec
DELETE FROM application_operation_step s WHERE s.workspace_id=sqlc.arg(workspace_id)::uuid AND (sqlc.narg(project_id)::uuid IS NULL OR s.operation_id IN (
 SELECT o.id FROM application_operation o JOIN application a ON a.id=o.application_id AND a.workspace_id=o.workspace_id
 WHERE a.workspace_id=sqlc.arg(workspace_id)::uuid AND a.project_id=sqlc.narg(project_id)::uuid));

-- name: DeleteScopedApplicationOperations :exec
DELETE FROM application_operation o WHERE o.workspace_id=sqlc.arg(workspace_id)::uuid AND (sqlc.narg(project_id)::uuid IS NULL OR o.application_id IN (
 SELECT a.id FROM application a WHERE a.workspace_id=sqlc.arg(workspace_id)::uuid AND a.project_id=sqlc.narg(project_id)::uuid));

-- name: DeleteScopedApplicationInstances :exec
DELETE FROM application_instance i WHERE i.workspace_id=sqlc.arg(workspace_id)::uuid AND (sqlc.narg(project_id)::uuid IS NULL OR i.application_id IN (
 SELECT a.id FROM application a WHERE a.workspace_id=sqlc.arg(workspace_id)::uuid AND a.project_id=sqlc.narg(project_id)::uuid));

-- name: DeleteScopedApplicationRelations :exec
DELETE FROM application_relation WHERE workspace_id=sqlc.arg(workspace_id)::uuid AND (sqlc.narg(project_id)::uuid IS NULL OR project_id=sqlc.narg(project_id)::uuid);

-- name: DeleteScopedApplicationRevisions :exec
DELETE FROM application_revision r WHERE r.workspace_id=sqlc.arg(workspace_id)::uuid AND (sqlc.narg(project_id)::uuid IS NULL OR r.application_id IN (
 SELECT a.id FROM application a WHERE a.workspace_id=sqlc.arg(workspace_id)::uuid AND a.project_id=sqlc.narg(project_id)::uuid));

-- name: DeleteScopedApplications :exec
DELETE FROM application WHERE workspace_id=sqlc.arg(workspace_id)::uuid AND (sqlc.narg(project_id)::uuid IS NULL OR project_id=sqlc.narg(project_id)::uuid);

-- name: DeleteRuntimeApplicationTickets :exec
DELETE FROM application_access_ticket t WHERE t.workspace_id=sqlc.arg(workspace_id)::uuid AND t.endpoint_id IN (
 SELECT e.id FROM application_endpoint e JOIN application_instance i ON i.id=e.instance_id AND i.workspace_id=e.workspace_id
 WHERE i.workspace_id=sqlc.arg(workspace_id)::uuid AND i.runtime_id=sqlc.arg(runtime_id)::uuid);

-- name: DeleteRuntimeApplicationConsumers :exec
DELETE FROM application_instance_consumer c WHERE c.workspace_id=sqlc.arg(workspace_id)::uuid AND (
 c.root_runtime_id=sqlc.arg(runtime_id)::uuid OR c.instance_id IN(SELECT i.id FROM application_instance i WHERE i.workspace_id=sqlc.arg(workspace_id)::uuid AND i.runtime_id=sqlc.arg(runtime_id)::uuid));

-- name: DeleteRuntimeApplicationEndpoints :exec
DELETE FROM application_endpoint e WHERE e.workspace_id=sqlc.arg(workspace_id)::uuid AND e.instance_id IN (
 SELECT i.id FROM application_instance i WHERE i.workspace_id=sqlc.arg(workspace_id)::uuid AND i.runtime_id=sqlc.arg(runtime_id)::uuid);

-- name: DeleteRuntimeApplicationInstances :exec
DELETE FROM application_instance WHERE workspace_id=sqlc.arg(workspace_id)::uuid AND runtime_id=sqlc.arg(runtime_id)::uuid;

-- name: DeleteApplicationTickets :exec
DELETE FROM application_access_ticket t WHERE t.workspace_id=$1 AND t.endpoint_id IN(SELECT e.id FROM application_endpoint e WHERE e.workspace_id=$1 AND e.application_id=$2);

-- name: DeleteApplicationOperationSteps :exec
DELETE FROM application_operation_step s WHERE s.workspace_id=$1 AND s.operation_id IN(SELECT o.id FROM application_operation o WHERE o.workspace_id=$1 AND o.application_id=$2);

-- name: DeleteApplicationOperations :exec
DELETE FROM application_operation WHERE workspace_id=$1 AND application_id=$2;
