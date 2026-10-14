-- Requirement lifecycle (1:1 with a kind='requirement' issue).

-- name: GetRequirementByIssue :one
SELECT * FROM requirement WHERE issue_id = @issue_id AND workspace_id = @workspace_id;

-- name: CreateRequirement :one
INSERT INTO requirement (workspace_id, issue_id, lifecycle_status,
    confirmed_revision, confirmed_by, confirmed_at, unconfirmed_change)
VALUES (@workspace_id, @issue_id, @lifecycle_status,
    @confirmed_revision, @confirmed_by, @confirmed_at, @unconfirmed_change)
RETURNING *;

-- name: ConfirmRequirement :one
-- Confirmation pins the current document revision. Any workspace member may
-- confirm; the confirmed actor is recorded (see confirmed_by).
UPDATE requirement SET lifecycle_status = 'confirmed',
    confirmed_revision = @confirmed_revision, confirmed_by = @confirmed_by,
    confirmed_at = now(), unconfirmed_change = false, updated_at = now()
WHERE issue_id = @issue_id AND workspace_id = @workspace_id
RETURNING *;

-- name: ReopenRequirementForChange :one
-- Editing the document after confirmation returns the requirement to
-- pending_confirm and flags an unconfirmed change.
UPDATE requirement SET lifecycle_status = 'pending_confirm', unconfirmed_change = true, updated_at = now()
WHERE issue_id = @issue_id AND workspace_id = @workspace_id
AND lifecycle_status = 'confirmed'
RETURNING *;

-- name: SetRequirementLifecycle :one
UPDATE requirement SET lifecycle_status = @lifecycle_status, unconfirmed_change = @unconfirmed_change, updated_at = now()
WHERE issue_id = @issue_id AND workspace_id = @workspace_id
RETURNING *;

-- name: ListRequirementSubIssues :many
-- Every sub-issue under a requirement across all 7-states, for the change-impact review.
SELECT * FROM issue
WHERE parent_issue_id = @requirement_issue_id AND workspace_id = @workspace_id
ORDER BY position, created_at;
