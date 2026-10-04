package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func loadProjectFailureRecoveryTarget(ctx context.Context, q *db.Queries, failed, source db.AgentTaskQueue) (*delegatedFailureRecoveryTarget, error) {
	if !failed.IssueID.Valid {
		return nil, nil
	}
	issue, err := q.GetIssue(ctx, failed.IssueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load failed project issue: %w", err)
	}
	if !issue.ProjectID.Valid {
		return nil, nil
	}
	project, err := q.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: issue.ProjectID, WorkspaceID: issue.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load failure recovery project: %w", err)
	}
	if project.Status != "in_progress" || project.LeadType.String != "agent" || !project.LeadID.Valid {
		return nil, nil
	}
	agent, err := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: project.LeadID, WorkspaceID: project.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load project recovery lead: %w", err)
	}
	if agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
		return nil, nil
	}
	return &delegatedFailureRecoveryTarget{failed: failed, source: source, issue: issue, agent: agent, project: &project}, nil
}
