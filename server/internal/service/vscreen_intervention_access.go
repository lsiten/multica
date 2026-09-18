package service

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Check source ownership inside the same transaction, including idempotent
// continuation retries, before returning IDs or consuming any native proof.
func (s *TaskService) authorizeInterventionSource(ctx context.Context, q *db.Queries, source db.AgentTaskQueue, workspaceID, actorID pgtype.UUID) error {
	switch {
	case source.IssueID.Valid:
		_, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: source.IssueID, WorkspaceID: workspaceID})
		return err
	case source.ChatSessionID.Valid:
		chat, err := q.LockChatSessionForEnqueue(ctx, source.ChatSessionID)
		if err != nil {
			return err
		}
		if chat.WorkspaceID != workspaceID || chat.AgentID != source.AgentID || chat.CreatorID != actorID {
			return InterventionError("permission_denied")
		}
	default:
		qc, ok := s.parseQuickCreateContext(source)
		if !ok || qc.WorkspaceID != util.UUIDToString(workspaceID) || qc.RequesterID != util.UUIDToString(actorID) {
			return InterventionError("permission_denied")
		}
	}
	return nil
}
