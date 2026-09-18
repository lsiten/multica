package service

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// CancelVscreenIntervention serializes explicit abandonment against continuation.
func (s *TaskService) CancelVscreenIntervention(ctx context.Context, row db.RuntimeVscreenIntervention, actorID pgtype.UUID) (db.RuntimeVscreenIntervention, error) {
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return row, err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	if _, err = q.LockVscreenWorkspace(ctx, row.WorkspaceID); err != nil {
		return row, err
	}
	if _, err = q.LockActiveMember(ctx, db.LockActiveMemberParams{UserID: actorID, WorkspaceID: row.WorkspaceID}); err != nil {
		return row, InterventionError("permission_denied")
	}
	rt, err := q.LockAgentRuntime(ctx, row.RuntimeID)
	if err != nil {
		return row, err
	}
	agent, err := q.GetAgentForClaimUpdate(ctx, row.AgentID)
	if err != nil {
		return row, err
	}
	if rt.WorkspaceID != row.WorkspaceID || agent.WorkspaceID != row.WorkspaceID || (rt.OwnerID != actorID && rt.Visibility != "public") {
		return row, InterventionError("permission_denied")
	}
	allowed, err := interventionInvokeAllowed(ctx, q, agent, actorID)
	if err != nil {
		return row, err
	}
	if !allowed {
		return row, InterventionError("permission_denied")
	}
	source, err := q.LockVscreenSourceTask(ctx, row.SourceTaskID)
	if err != nil {
		return row, err
	}
	if source.AgentID != row.AgentID || source.RuntimeID != row.RuntimeID {
		return row, InterventionError("source_mismatch")
	}
	if err = s.authorizeInterventionSource(ctx, q, source, row.WorkspaceID, actorID); err != nil {
		return row, err
	}
	row, err = q.LockVscreenIntervention(ctx, db.LockVscreenInterventionParams{ID: row.ID, WorkspaceID: row.WorkspaceID})
	if err != nil {
		return row, err
	}
	if row.State == "continued" {
		return row, InterventionError("already_continued")
	}
	if row.State != "cancelled" {
		row, err = q.UpdateVscreenIntervention(ctx, db.UpdateVscreenInterventionParams{ID: row.ID, WorkspaceID: row.WorkspaceID, State: "cancelled", ReturnReceiptID: row.ReturnReceiptID, LastActionID: row.LastActionID, GeometryRevision: row.GeometryRevision})
		if err != nil {
			return row, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return row, err
	}
	return row, nil
}
