package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	generated "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func (h *Handler) StopAgentRun(w http.ResponseWriter, r *http.Request) {
	defer collaborationTrace(r.Context(), "stop")()
	task, ok := h.loadTaskForUser(w, r)
	if !ok {
		return
	}
	var req agentCollaborationCommand
	if !decodeAgentCollaborationCommand(w, r, &req) {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, ctxWorkspaceID(r.Context()), "workspace id")
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, requestUserID(r), uuidToString(workspaceID))
	actorUUID, ok := parseUUIDOrBadRequest(w, actorID, "actor_id")
	if !ok {
		return
	}
	if actorType == "agent" && !h.taskSourceOrEmpty(r, actorType, actorID).Valid {
		writeError(w, http.StatusForbidden, "an active source Run is required")
		return
	}
	targetAgent, targetErr := h.Queries.GetAgent(r.Context(), task.AgentID)
	if targetErr != nil || !h.canInvokeAgent(r.Context(), targetAgent, actorType, actorID, h.collaborationOriginator(r, actorType, actorID), uuidToString(workspaceID)) {
		writeError(w, http.StatusForbidden, "agent stop is not allowed")
		return
	}
	fp := collaborationFingerprint("stop", req.ClientRequestID, uuidToString(task.ID), actorType, actorID)
	reservation := task
	reservation.ID = h.taskSourceOrEmpty(r, actorType, actorID)
	op, replay, err := h.reserveCollaborationOperation(r.Context(), workspaceID, actorType, actorUUID, req.ClientRequestID, "stop", fp, reservation, task.AgentID)
	if err != nil {
		h.writeCollaborationError(w, err)
		return
	}
	if !replay {
		if op, err = h.Queries.SetAgentCollaborationOperationTargets(r.Context(), generated.SetAgentCollaborationOperationTargetsParams{ID: op.ID, IssueID: task.IssueID, TargetAgentID: task.AgentID, TargetTaskID: task.ID}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save stop receipt")
			return
		}
		cancelled, cancelErr := h.TaskService.CancelTaskWithResult(r.Context(), task.ID, service.CancelTaskOptions{CancelledBy: h.taskCancellationActor(r.Context(), actorType, actorID), UserInitiated: true})
		if cancelErr != nil {
			h.failCollaborationOperation(r.Context(), op.ID, cancelErr.Error())
			h.writeCollaborationError(w, cancelErr)
			return
		}
		result, _ := json.Marshal(map[string]any{"task_id": uuidToString(cancelled.Task.ID), "status": cancelled.Task.Status})
		if _, err = h.Queries.CompleteAgentCollaborationOperation(r.Context(), generated.CompleteAgentCollaborationOperationParams{ID: op.ID, Status: "stop_requested", Result: result}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save stop receipt")
			return
		}
		op.Status = "stop_requested"
		op.Result = result
	}
	writeJSON(w, http.StatusAccepted, collaborationOperationResponse(op, task, generated.CreateCommentRow{}))
}

type collaborationTaskInput struct {
	Operation, Fingerprint, ClientRequestID, ActorType, Originator, Content string
	ActorID, SourceTaskID                                                   pgtype.UUID
	Issue                                                                   generated.Issue
	Target                                                                  generated.Agent
	ForceFresh                                                              bool
	FireAt                                                                  pgtype.Timestamptz
}

func (h *Handler) createCollaborationTask(r *http.Request, in collaborationTaskInput) (generated.AgentCollaborationOperation, generated.AgentTaskQueue, generated.CreateCommentRow, bool, error) {
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		return generated.AgentCollaborationOperation{}, generated.AgentTaskQueue{}, generated.CreateCommentRow{}, false, err
	}
	defer tx.Rollback(r.Context())
	q := h.Queries.WithTx(tx)
	op, replay, err := h.reserveCollaborationOperationTx(r.Context(), q, in.Issue.WorkspaceID, in.ActorType, in.ActorID, in.ClientRequestID, in.Operation, in.Fingerprint, generated.AgentTaskQueue{ID: in.SourceTaskID, IssueID: in.Issue.ID}, in.Target.ID)
	if err != nil || replay {
		return op, generated.AgentTaskQueue{}, generated.CreateCommentRow{}, replay, err
	}
	comment, err := q.CreateComment(r.Context(), generated.CreateCommentParams{IssueID: in.Issue.ID, WorkspaceID: in.Issue.WorkspaceID, AuthorType: in.ActorType, AuthorID: in.ActorID, Content: in.Content, Type: "comment", SourceTaskID: in.SourceTaskID, ID: dbid.NewV7()})
	if err != nil {
		return op, generated.AgentTaskQueue{}, generated.CreateCommentRow{}, false, err
	}
	originator := pgtype.UUID{}
	if in.Originator != "" {
		originator = parseUUID(in.Originator)
	}
	task, err := q.CreateAgentTask(r.Context(), generated.CreateAgentTaskParams{ID: dbid.NewV7(), AgentID: in.Target.ID, RuntimeID: in.Target.RuntimeID, IssueID: in.Issue.ID, Priority: collaborationPriority(in.Issue.Priority), FireAt: in.FireAt, TriggerCommentID: comment.ID, TriggerSummary: pgtype.Text{String: collaborationTruncate(in.Content, 200), Valid: true}, ForceFreshSession: pgtype.Bool{Bool: in.ForceFresh, Valid: true}, OriginatorUserID: originator, AccountableUserID: originator, OriginatorSource: pgtype.Text{String: "agent_collaboration", Valid: true}, DelegatedFromTaskID: in.SourceTaskID, TriggerEvidenceKind: pgtype.Text{String: "agent_collaboration", Valid: true}, TriggerEvidenceRefID: op.ID})
	if err != nil {
		return op, generated.AgentTaskQueue{}, generated.CreateCommentRow{}, false, err
	}
	if op, err = q.SetAgentCollaborationOperationTargets(r.Context(), generated.SetAgentCollaborationOperationTargetsParams{ID: op.ID, IssueID: in.Issue.ID, TargetAgentID: in.Target.ID, TargetTaskID: task.ID, CommentID: comment.ID}); err != nil {
		return op, generated.AgentTaskQueue{}, generated.CreateCommentRow{}, false, err
	}
	result, _ := json.Marshal(map[string]any{"task_id": uuidToString(task.ID), "comment_id": uuidToString(comment.ID)})
	if _, err = q.CompleteAgentCollaborationOperation(r.Context(), generated.CompleteAgentCollaborationOperationParams{ID: op.ID, Status: "accepted", Result: result}); err != nil {
		return op, generated.AgentTaskQueue{}, generated.CreateCommentRow{}, false, err
	}
	if err = tx.Commit(r.Context()); err != nil {
		return op, generated.AgentTaskQueue{}, generated.CreateCommentRow{}, false, err
	}
	op.Status = "accepted"
	op.TargetTaskID = task.ID
	op.CommentID = comment.ID
	op.Result = result
	return op, task, comment, false, nil
}

func (h *Handler) bindCollaborationSupplement(r *http.Request, task generated.AgentTaskQueue, issue generated.Issue, req agentCollaborationCommand, actorType string, actorID, sourceTaskID pgtype.UUID, originator, fingerprint string) (generated.AgentCollaborationOperation, generated.CreateCommentRow, bool, error) {
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		return generated.AgentCollaborationOperation{}, generated.CreateCommentRow{}, false, err
	}
	defer tx.Rollback(r.Context())
	q := h.Queries.WithTx(tx)
	reservation := task
	reservation.ID = sourceTaskID
	op, replay, err := h.reserveCollaborationOperationTx(r.Context(), q, issue.WorkspaceID, actorType, actorID, req.ClientRequestID, "append", fingerprint, reservation, task.AgentID)
	if err != nil || replay {
		return op, generated.CreateCommentRow{}, replay, err
	}
	comment, err := q.CreateComment(r.Context(), generated.CreateCommentParams{IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, AuthorType: actorType, AuthorID: actorID, Content: req.Content, Type: "comment", SourceTaskID: sourceTaskID, ID: dbid.NewV7()})
	if err != nil {
		return op, generated.CreateCommentRow{}, false, err
	}
	if _, err = q.LockTaskForSupplementBind(r.Context(), task.ID); err != nil {
		return op, generated.CreateCommentRow{}, false, err
	}
	if _, err = q.BindCommentTaskSupplement(r.Context(), generated.BindCommentTaskSupplementParams{TaskID: task.ID, IssueID: issue.ID, AgentID: task.AgentID, WorkspaceID: issue.WorkspaceID, AuthorType: actorType, MaxAgentBindings: maxCollaborationAgentBindings, AuthorID: actorID, MaxBindingsPerAgent: maxCollaborationBindingsPerAgent, CommentID: comment.ID, ClientRequestID: op.ID}); err != nil {
		return op, generated.CreateCommentRow{}, false, err
	}
	if op, err = q.SetAgentCollaborationOperationTargets(r.Context(), generated.SetAgentCollaborationOperationTargetsParams{ID: op.ID, IssueID: issue.ID, TargetAgentID: task.AgentID, TargetTaskID: task.ID, CommentID: comment.ID}); err != nil {
		return op, generated.CreateCommentRow{}, false, err
	}
	result, _ := json.Marshal(map[string]any{"task_id": uuidToString(task.ID), "comment_id": uuidToString(comment.ID)})
	if _, err = q.CompleteAgentCollaborationOperation(r.Context(), generated.CompleteAgentCollaborationOperationParams{ID: op.ID, Status: "accepted", Result: result}); err != nil {
		return op, generated.CreateCommentRow{}, false, err
	}
	if err = tx.Commit(r.Context()); err != nil {
		return op, generated.CreateCommentRow{}, false, err
	}
	op.Status = "accepted"
	op.TargetTaskID = task.ID
	op.CommentID = comment.ID
	op.Result = result
	return op, comment, false, nil
}

func (h *Handler) reserveCollaborationOperation(ctx context.Context, workspace pgtype.UUID, actorType string, actorID pgtype.UUID, clientID, operation, fingerprint string, task generated.AgentTaskQueue, targetAgent pgtype.UUID) (generated.AgentCollaborationOperation, bool, error) {
	if strings.TrimSpace(clientID) == "" || len(clientID) > 200 {
		return generated.AgentCollaborationOperation{}, false, errors.New("client_request_id is required")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return generated.AgentCollaborationOperation{}, false, err
	}
	defer tx.Rollback(ctx)
	q := h.Queries.WithTx(tx)
	op, replay, err := h.reserveCollaborationOperationTx(ctx, q, workspace, actorType, actorID, clientID, operation, fingerprint, task, targetAgent)
	if err != nil || replay {
		return op, replay, err
	}
	if err = tx.Commit(ctx); err != nil {
		return generated.AgentCollaborationOperation{}, false, err
	}
	return op, false, nil
}

func (h *Handler) reserveCollaborationOperationTx(ctx context.Context, q *generated.Queries, workspace pgtype.UUID, actorType string, actorID pgtype.UUID, clientID, operation, fingerprint string, task generated.AgentTaskQueue, targetAgent pgtype.UUID) (generated.AgentCollaborationOperation, bool, error) {
	if strings.TrimSpace(clientID) == "" || len(clientID) > 200 {
		return generated.AgentCollaborationOperation{}, false, errors.New("client_request_id is required")
	}
	op, err := q.CreateAgentCollaborationOperation(ctx, generated.CreateAgentCollaborationOperationParams{ID: dbid.NewV7(), WorkspaceID: workspace, ActorType: actorType, ActorID: actorID, ClientRequestID: clientID, Operation: operation, Fingerprint: fingerprint, Status: "accepted", SourceTaskID: task.ID, IssueID: task.IssueID, TargetAgentID: targetAgent})
	if errors.Is(err, pgx.ErrNoRows) {
		op, err = q.GetAgentCollaborationOperationByKey(ctx, generated.GetAgentCollaborationOperationByKeyParams{WorkspaceID: workspace, ActorType: actorType, ActorID: actorID, ClientRequestID: clientID})
		if err == nil && op.Fingerprint != fingerprint {
			return generated.AgentCollaborationOperation{}, false, errors.New("client_request_id was already used with different content")
		}
		if err == nil {
			return op, true, nil
		}
	}
	if err != nil {
		return generated.AgentCollaborationOperation{}, false, err
	}
	return op, false, nil
}
