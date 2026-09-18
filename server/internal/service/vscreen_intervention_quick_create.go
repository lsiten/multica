package service

import (
	"context"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func (s *TaskService) enqueueQuickCreateIntervention(ctx context.Context, q *db.Queries, in interventionEnqueue) (db.AgentTaskQueue, error) {
	var empty db.AgentTaskQueue
	qc, ok := s.parseQuickCreateContext(in.Source)
	if !ok || qc.RequesterID != util.UUIDToString(in.Request.ActorID) || qc.WorkspaceID != util.UUIDToString(in.Request.Intervention.WorkspaceID) {
		return empty, InterventionError("permission_denied")
	}
	if err := CheckIssueCreateCapacity(ctx, q, s.Entitlements, in.Request.Intervention.WorkspaceID); err != nil {
		return empty, err
	}
	var capture db.IssueSourceContext
	if qc.SourceContextID != "" {
		contextID, err := util.ParseUUID(qc.SourceContextID)
		if err != nil {
			return empty, InterventionError("source_mismatch")
		}
		capture, err = q.GetPendingIssueSourceContextByOriginTask(ctx, db.GetPendingIssueSourceContextByOriginTaskParams{WorkspaceID: in.Request.Intervention.WorkspaceID, OriginTaskID: in.Source.ID})
		if err != nil {
			return empty, err
		}
		if capture.ID != contextID {
			return empty, InterventionError("source_mismatch")
		}
	}
	child, err := q.CreateManualQuickCreateRetryTask(ctx, db.CreateManualQuickCreateRetryTaskParams{ActorUserID: in.Request.ActorID, RuntimeMcpOverlay: in.Prepared.runtimeOverlay.Overlay, RuntimeConnectedApps: in.Prepared.runtimeOverlay.ConnectedApps, NewTaskID: dbid.NewV7(), SourceTaskID: in.Source.ID})
	if err != nil {
		return empty, err
	}
	if capture.ID.Valid {
		if _, err = q.TransferPendingIssueSourceContextTask(ctx, db.TransferPendingIssueSourceContextTaskParams{NewTaskID: child.ID, WorkspaceID: in.Request.Intervention.WorkspaceID, ID: capture.ID, OldTaskID: in.Source.ID}); err != nil {
			return empty, err
		}
	}
	return child, nil
}
