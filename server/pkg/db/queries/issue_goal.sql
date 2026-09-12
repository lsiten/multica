-- name: GetIssueGoal :one
SELECT issue_id, objective, completed_at, completed_by_type, completed_by_id, created_at, updated_at
FROM issue_goal
WHERE issue_id = $1;

-- name: DeleteIssueGoal :exec
DELETE FROM issue_goal
WHERE issue_id = $1;

-- name: DeleteIssueGoalsForWorkspace :exec
-- issue_goal has no workspace_id or foreign key; resolve ownership through issue.
DELETE FROM issue_goal
WHERE issue_id IN (
    SELECT id FROM issue WHERE workspace_id = sqlc.arg('workspace_id')::uuid
);

-- name: UpsertIssueGoal :one
INSERT INTO issue_goal (issue_id, objective)
VALUES ($1, $2)
ON CONFLICT (issue_id) DO UPDATE SET
    objective = EXCLUDED.objective,
    completed_at = NULL,
    completed_by_type = NULL,
    completed_by_id = NULL,
    updated_at = now()
RETURNING issue_id, objective, completed_at, completed_by_type, completed_by_id, created_at, updated_at;

-- name: CompleteIssueGoal :one
UPDATE issue_goal
SET completed_at = now(),
    completed_by_type = $2,
    completed_by_id = $3,
    updated_at = now()
WHERE issue_id = $1
  AND completed_at IS NULL
RETURNING issue_id, objective, completed_at, completed_by_type, completed_by_id, created_at, updated_at;
