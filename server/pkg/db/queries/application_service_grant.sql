-- name: LockApplicationServiceRuntimeOwner :one
SELECT r.id AS runtime_id, r.workspace_id, r.daemon_id, r.owner_id, m.id AS member_id
FROM agent_runtime r JOIN member m ON m.workspace_id=r.workspace_id AND m.user_id=r.owner_id
WHERE r.id= @runtime_id AND r.workspace_id= @workspace_id AND r.daemon_id= @daemon_id
FOR SHARE OF r,m;

-- name: GetApplicationServiceAuthority :one
SELECT * FROM application_service_authority WHERE runtime_id=$1;

-- name: CreateApplicationServiceAuthority :one
INSERT INTO application_service_authority(runtime_id,workspace_id,daemon_id,owner_id,member_id,service_instance_id,generation)
VALUES ($1,$2,$3,$4,$5,$6,1)
ON CONFLICT(runtime_id) DO NOTHING RETURNING *;

-- name: ReplaceApplicationServiceAuthority :one
UPDATE application_service_authority
SET workspace_id= @workspace_id, daemon_id= @daemon_id, owner_id= @owner_id,
 member_id= @member_id, service_instance_id= @service_instance_id,
 generation=generation+1, revoked=false, updated_at=now()
WHERE runtime_id= @runtime_id AND generation= @expected_generation
RETURNING *;

-- name: RevokeApplicationServiceAuthority :one
UPDATE application_service_authority SET revoked=true,updated_at=now()
WHERE runtime_id= @runtime_id AND workspace_id= @workspace_id AND daemon_id= @daemon_id
 AND service_instance_id= @service_instance_id AND generation= @generation
RETURNING *;

-- name: CreateApplicationServiceGrant :exec
INSERT INTO application_service_grant(token_hash,runtime_id,generation,operations,expires_at)
VALUES ($1,$2,$3,$4,$5);

-- name: GetApplicationServiceGrant :one
SELECT g.*, a.workspace_id, a.daemon_id, a.owner_id, a.member_id, a.service_instance_id
FROM application_service_grant g
JOIN application_service_authority a ON a.runtime_id=g.runtime_id AND a.generation=g.generation
JOIN agent_runtime r ON r.id=a.runtime_id AND r.workspace_id=a.workspace_id AND r.daemon_id=a.daemon_id AND r.owner_id=a.owner_id
JOIN member m ON m.id=a.member_id AND m.workspace_id=a.workspace_id AND m.user_id=a.owner_id
WHERE g.token_hash=$1 AND NOT a.revoked AND g.expires_at>clock_timestamp();

-- name: LockApplicationServiceGrant :one
SELECT g.*, a.workspace_id, a.daemon_id, a.owner_id, a.member_id, a.service_instance_id
FROM application_service_grant g
JOIN application_service_authority a ON a.runtime_id=g.runtime_id AND a.generation=g.generation
JOIN agent_runtime r ON r.id=a.runtime_id AND r.workspace_id=a.workspace_id AND r.daemon_id=a.daemon_id AND r.owner_id=a.owner_id
JOIN member m ON m.id=a.member_id AND m.workspace_id=a.workspace_id AND m.user_id=a.owner_id
WHERE g.token_hash=$1 AND NOT a.revoked AND g.expires_at>clock_timestamp()
FOR SHARE OF a,g;
