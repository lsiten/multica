package service

import (
	"context"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// InterventionContinuation binds a human request to a fresh daemon observation and the pre-query DB version.
type InterventionContinuation struct {
	Intervention db.RuntimeVscreenIntervention
	ActorID      pgtype.UUID
	HumanSummary string
	FreshSession bool
	Observation  protocol.VscreenQueryResult
}

// ContinueAfterIntervention creates and consumes in one transaction; notifications occur only after commit.
func (s *TaskService) ContinueAfterIntervention(ctx context.Context, request InterventionContinuation) (db.AgentTaskQueue, error) {
	var empty db.AgentTaskQueue
	before := request.Intervention
	if len(request.HumanSummary) > 2048 || !utf8.ValidString(request.HumanSummary) {
		return empty, InterventionError("invalid_summary")
	}
	prepared, err := s.PrepareChatTaskEnqueue(ctx, before.AgentID, request.ActorID)
	if err != nil {
		if err == ErrChatTaskAgentArchived || err == ErrChatTaskAgentNoRuntime {
			return empty, InterventionError("permission_denied")
		}
		return empty, err
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	if _, err = q.LockVscreenWorkspace(ctx, before.WorkspaceID); err != nil {
		return empty, err
	}
	if _, err = q.LockActiveMember(ctx, db.LockActiveMemberParams{UserID: request.ActorID, WorkspaceID: before.WorkspaceID}); err != nil {
		return empty, InterventionError("permission_denied")
	}
	rt, err := q.LockAgentRuntime(ctx, before.RuntimeID)
	if err != nil {
		return empty, err
	}
	agent, err := q.GetAgentForClaimUpdate(ctx, before.AgentID)
	if err != nil {
		return empty, err
	}
	if rt.WorkspaceID != before.WorkspaceID || agent.WorkspaceID != before.WorkspaceID || agent.RuntimeID != rt.ID || agent.ArchivedAt.Valid || (rt.OwnerID != request.ActorID && rt.Visibility != "public") {
		return empty, InterventionError("permission_denied")
	}
	allowed, err := interventionInvokeAllowed(ctx, q, agent, request.ActorID)
	if err != nil {
		return empty, err
	}
	if !allowed {
		return empty, InterventionError("permission_denied")
	}
	source, err := q.LockVscreenSourceTask(ctx, before.SourceTaskID)
	if err != nil {
		return empty, err
	}
	if source.AgentID != agent.ID || source.RuntimeID != rt.ID {
		return empty, InterventionError("source_mismatch")
	}
	if err = s.authorizeInterventionSource(ctx, q, source, before.WorkspaceID, request.ActorID); err != nil {
		return empty, err
	}
	row, err := q.LockVscreenIntervention(ctx, db.LockVscreenInterventionParams{ID: before.ID, WorkspaceID: before.WorkspaceID})
	if err != nil {
		return empty, err
	}
	if row.SourceTaskID != source.ID || row.AgentID != agent.ID || row.RuntimeID != rt.ID {
		return empty, InterventionError("source_mismatch")
	}
	if row.State == "continued" && row.ContinuationTaskID.Valid {
		return q.GetAgentTask(ctx, row.ContinuationTaskID)
	}
	if row.State != "ready_to_continue" || row.Version != before.Version {
		return empty, InterventionError("intervention_changed")
	}
	if !interventionProofMatches(row, request.Observation) {
		return empty, InterventionError("return_unverified")
	}
	if source.Status != "failed" || !source.CompletedAt.Valid || source.FailureReason.String != string(taskfailure.ReasonGUIHumanIntervention) {
		return empty, InterventionError("source_not_stopped")
	}
	if !request.FreshSession && (source.SessionRolloutMissing || !source.SessionID.Valid || source.SessionID.String == "" || !source.WorkDir.Valid || source.WorkDir.String == "" || ResumeUnsafeFailure(source.FailureReason.String, source.Error.String)) {
		return empty, InterventionError("resume_unavailable")
	}
	chat, err := s.interventionScope(ctx, q, source, row.WorkspaceID)
	if err != nil {
		return empty, err
	}
	task, err := s.enqueueIntervention(ctx, q, interventionEnqueue{Source: source, Chat: chat, Prepared: prepared, Request: request})
	if err != nil {
		return empty, err
	}
	if _, err = q.ConsumeVscreenIntervention(ctx, db.ConsumeVscreenInterventionParams{ID: row.ID, WorkspaceID: row.WorkspaceID, ContinuationTaskID: task.ID, HumanSummary: request.HumanSummary, Version: row.Version}); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	if task.ChatSessionID.Valid {
		s.FinalizeChatTaskEnqueue(ctx, task)
	} else {
		s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
		s.NotifyTaskEnqueued(ctx, task)
	}
	return task, nil
}

func interventionProofMatches(row db.RuntimeVscreenIntervention, proof protocol.VscreenQueryResult) bool {
	if proof.Validate("state") != nil || proof.Reason != "" || proof.State == nil {
		return false
	}
	state := proof.State
	return proof.WorkspaceID == util.UUIDToString(row.WorkspaceID) && proof.RuntimeID == util.UUIDToString(row.RuntimeID) && state.State == protocol.VscreenStateReady && state.ActiveTaskID == nil && state.InterventionID != nil && *state.InterventionID == util.UUIDToString(row.ID) && state.InterventionState == protocol.VscreenInterventionReadyToContinue && state.ReturnReceiptID == row.ReturnReceiptID && row.ReturnReceiptID != "" && state.NativeEpoch == row.NativeEpoch && state.DisplayGeneration == row.DisplayGeneration && state.GeometryRevision == uint64(row.GeometryRevision) && state.Permissions.Accessibility == "granted" && state.Permissions.ScreenRecording == "granted"
}

func (s *TaskService) interventionScope(ctx context.Context, q *db.Queries, source db.AgentTaskQueue, workspaceID pgtype.UUID) (db.ChatSession, error) {
	var chat db.ChatSession
	switch {
	case source.IssueID.Valid:
		if _, err := q.LockIssueForDelete(ctx, db.LockIssueForDeleteParams{ID: source.IssueID, WorkspaceID: workspaceID}); err != nil {
			return chat, err
		}
		issue, err := q.GetIssue(ctx, source.IssueID)
		if err != nil {
			return chat, err
		}
		status, err := issuestatus.Resolve(ctx, q, workspaceID, issue.Status)
		if err != nil {
			return chat, InterventionError("source_inactive")
		}
		if status.Category == issuestatus.Done || status.Category == issuestatus.Cancelled {
			return chat, InterventionError("source_inactive")
		}
	case source.ChatSessionID.Valid:
		var err error
		chat, err = q.LockChatSessionForEnqueue(ctx, source.ChatSessionID)
		if err != nil {
			return chat, err
		}
		if chat.WorkspaceID != workspaceID || chat.AgentID != source.AgentID || chat.Status != "active" {
			return chat, InterventionError("source_inactive")
		}
	default:
		qc, ok := s.parseQuickCreateContext(source)
		if !ok || qc.WorkspaceID != util.UUIDToString(workspaceID) {
			return chat, InterventionError("unsupported_task_scope")
		}
	}
	return chat, nil
}

func interventionInvokeAllowed(ctx context.Context, q *db.Queries, agent db.Agent, userID pgtype.UUID) (bool, error) {
	if agent.OwnerID == userID {
		return true, nil
	}
	if agent.PermissionMode != "public_to" {
		return false, nil
	}
	targets, err := q.LockVscreenInvocationTargets(ctx, agent.ID)
	if err != nil {
		return false, err
	}
	for _, target := range targets {
		if target.TargetType == "workspace" || target.TargetType == "member" && target.TargetID == userID {
			return true, nil
		}
	}
	return false, nil
}
