package handler

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Runtime visibility does not expose a different member's chat or quick-create
// task. Keep the same source ownership and private-agent gates as those pages.
func (h *Handler) canReadVscreenIntervention(ctx context.Context, userID string, row db.RuntimeVscreenIntervention) (bool, error) {
	if userID == "" {
		return false, nil
	}
	source, err := h.Queries.GetAgentTask(ctx, row.SourceTaskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if source.AgentID != row.AgentID || source.RuntimeID != row.RuntimeID {
		return false, nil
	}
	if source.IssueID.Valid {
		_, err = h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: source.IssueID, WorkspaceID: row.WorkspaceID})
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	}
	if source.ChatSessionID.Valid {
		chat, err := h.Queries.GetChatSessionInWorkspace(ctx, db.GetChatSessionInWorkspaceParams{ID: source.ChatSessionID, WorkspaceID: row.WorkspaceID})
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if uuidToString(chat.CreatorID) != userID || chat.AgentID != row.AgentID {
			return false, nil
		}
	} else {
		var qc service.QuickCreateContext
		if json.Unmarshal(source.Context, &qc) != nil || qc.Type != "quick_create" || qc.WorkspaceID != uuidToString(row.WorkspaceID) || qc.RequesterID != userID {
			return false, nil
		}
	}
	agent, err := h.Queries.GetAgent(ctx, row.AgentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return h.canAccessPrivateAgent(ctx, agent, "member", userID, uuidToString(row.WorkspaceID)), nil
}
