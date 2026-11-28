package handler

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectaccess"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Resolve the same issue, autopilot and quick-create scopes as task detail reads.
func (h *Handler) hiddenJevDecisionTasks(ctx context.Context, workspaceID pgtype.UUID, hidden projectaccess.Hidden) ([]pgtype.UUID, error) {
	if hidden.Empty() {
		return nil, nil
	}
	scopes, err := h.Queries.ListJevDecisionTaskScopes(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	tasks := make([]db.AgentTaskQueue, len(scopes))
	for index, scope := range scopes {
		tasks[index] = db.AgentTaskQueue{ID: scope.ID, IssueID: scope.IssueID, ChatSessionID: scope.ChatSessionID, AutopilotRunID: scope.AutopilotRunID, Context: scope.Context}
	}
	projects, err := projectaccess.TasksProjects(ctx, h.Queries, tasks)
	if err != nil {
		return nil, err
	}
	var ids []pgtype.UUID
	for index, scope := range projects {
		if !hidden.CanSeeProjects(scope) {
			ids = append(ids, tasks[index].ID)
		}
	}
	return ids, nil
}
