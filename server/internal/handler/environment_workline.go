package handler

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) automationPriorWorkdir(ctx context.Context, task db.AgentTaskQueue, workspaceID, autopilotID pgtype.UUID) (pgtype.Text, error) {
	if task.RerunOfTaskID.Valid {
		source, err := h.Queries.GetAgentTask(ctx, task.RerunOfTaskID)
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.Text{}, nil
		}
		if err != nil {
			return pgtype.Text{}, err
		}
		if source.AgentID != task.AgentID || source.RuntimeID != task.RuntimeID || source.IssueID.Valid || source.ChatSessionID.Valid || !source.AutopilotRunID.Valid {
			return pgtype.Text{}, nil
		}
		run, err := h.Queries.GetAutopilotRun(ctx, source.AutopilotRunID)
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.Text{}, nil
		}
		if err != nil {
			return pgtype.Text{}, err
		}
		if run.AutopilotID != autopilotID {
			return pgtype.Text{}, nil
		}
		return source.WorkDir, nil
	}
	workdir, err := h.Queries.GetLastAutomationWorkdir(ctx, db.GetLastAutomationWorkdirParams{WorkspaceID: workspaceID, AutopilotID: autopilotID, AgentID: task.AgentID, RuntimeID: task.RuntimeID, CurrentTaskID: task.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.Text{}, nil
	}
	return workdir, err
}
