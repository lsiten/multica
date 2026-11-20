package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	generated "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	collaborationOperationMaxBody    = 64 << 10
	maxCollaborationAgentBindings    = 10
	maxCollaborationBindingsPerAgent = 3
)

type agentCollaborationCommand struct {
	ClientRequestID string `json:"client_request_id"`
	TargetAgentID   string `json:"target_agent_id"`
	Content         string `json:"content"`
}

func (h *Handler) DelegateAgent(w http.ResponseWriter, r *http.Request) {
	defer collaborationTrace(r.Context(), "delegate")()
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req agentCollaborationCommand
	if !decodeAgentCollaborationCommand(w, r, &req) {
		return
	}
	targetID, ok := parseUUIDOrBadRequest(w, req.TargetAgentID, "target_agent_id")
	if !ok {
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	actorType, actorID := h.resolveActor(r, requestUserID(r), uuidToString(issue.WorkspaceID))
	actorUUID, ok := parseUUIDOrBadRequest(w, actorID, "actor_id")
	if !ok {
		return
	}
	target, err := h.Queries.GetAgentInWorkspace(r.Context(), generated.GetAgentInWorkspaceParams{ID: targetID, WorkspaceID: issue.WorkspaceID})
	if err != nil || !target.RuntimeID.Valid {
		writeError(w, http.StatusConflict, "target agent is unavailable")
		return
	}
	if err := h.ensureCollaborationProjectScope(r.Context(), issue, target); err != nil {
		h.writeCollaborationError(w, err)
		return
	}
	originator := h.collaborationOriginator(r, actorType, actorID)
	if !h.canInvokeAgent(r.Context(), target, actorType, actorID, originator, uuidToString(issue.WorkspaceID)) {
		writeError(w, http.StatusForbidden, "agent invocation is not allowed")
		return
	}
	sourceTaskID := h.collaborationSourceTask(r, actorType, actorID)
	if actorType == "agent" && !sourceTaskID.Valid {
		writeError(w, http.StatusForbidden, "an active source Run is required")
		return
	}
	fingerprint := collaborationFingerprint("delegate", req.ClientRequestID, uuidToString(target.ID), uuidToString(issue.ID), req.Content, uuidToString(sourceTaskID))
	op, task, comment, replay, err := h.createCollaborationTask(r, collaborationTaskInput{
		Operation: "delegate", Fingerprint: fingerprint, ClientRequestID: req.ClientRequestID,
		ActorType: actorType, ActorID: actorUUID, SourceTaskID: sourceTaskID,
		Issue: issue, Target: target, Content: req.Content, ForceFresh: true,
		Originator: originator, FireAt: pgtype.Timestamptz{},
	})
	if err != nil {
		h.writeCollaborationError(w, err)
		return
	}
	if !replay {
		h.TaskService.NotifyTaskEnqueued(r.Context(), task)
		h.publishCollaborationComment(issue, actorType, actorUUID, comment)
	}
	writeJSON(w, http.StatusAccepted, collaborationOperationResponse(op, task, comment))
}

func (h *Handler) AppendAgent(w http.ResponseWriter, r *http.Request) {
	defer collaborationTrace(r.Context(), "append")()
	task, ok := h.loadTaskForUser(w, r)
	if !ok {
		return
	}
	var req agentCollaborationCommand
	if !decodeAgentCollaborationCommand(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "content is required")
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
	target, err := h.Queries.GetAgent(r.Context(), task.AgentID)
	if err != nil {
		writeError(w, http.StatusNotFound, "target agent not found")
		return
	}
	originator := h.collaborationOriginator(r, actorType, actorID)
	if !h.canInvokeAgent(r.Context(), target, actorType, actorID, originator, uuidToString(workspaceID)) {
		writeError(w, http.StatusForbidden, "agent invocation is not allowed")
		return
	}
	if !task.IssueID.Valid {
		writeError(w, http.StatusConflict, "target run cannot receive collaboration input")
		return
	}
	issue, err := h.Queries.GetIssue(r.Context(), task.IssueID)
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if err := h.ensureCollaborationProjectScope(r.Context(), issue, target); err != nil {
		h.writeCollaborationError(w, err)
		return
	}
	sourceTaskID := h.collaborationSourceTask(r, actorType, actorID)
	if actorType == "agent" && !sourceTaskID.Valid {
		writeError(w, http.StatusForbidden, "an active source Run is required")
		return
	}
	fingerprint := collaborationFingerprint("append", req.ClientRequestID, uuidToString(task.ID), req.Content, actorType, actorID)
	op, comment, replay, err := h.bindCollaborationSupplement(r, task, issue, req, actorType, actorUUID, sourceTaskID, originator, fingerprint)
	if err != nil {
		h.writeCollaborationError(w, err)
		return
	}
	if !replay && h.DaemonTaskSupplement != nil && task.RuntimeID.Valid {
		h.DaemonTaskSupplement.NotifyTaskSupplementAvailable(uuidToString(task.RuntimeID), uuidToString(task.ID))
	}
	if !replay {
		h.publishCollaborationComment(issue, actorType, actorUUID, comment)
	}
	writeJSON(w, http.StatusAccepted, collaborationOperationResponse(op, task, comment))
}

func (h *Handler) NextAgentTurn(w http.ResponseWriter, r *http.Request) {
	defer collaborationTrace(r.Context(), "next")()
	base, ok := h.loadTaskForUser(w, r)
	if !ok {
		return
	}
	var req agentCollaborationCommand
	if !decodeAgentCollaborationCommand(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if !base.IssueID.Valid {
		writeError(w, http.StatusConflict, "target run has no issue context")
		return
	}
	issue, err := h.Queries.GetIssue(r.Context(), base.IssueID)
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
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
	target, err := h.Queries.GetAgent(r.Context(), base.AgentID)
	if err != nil {
		writeError(w, http.StatusNotFound, "target agent not found")
		return
	}
	if err := h.ensureCollaborationProjectScope(r.Context(), issue, target); err != nil {
		h.writeCollaborationError(w, err)
		return
	}
	originator := h.collaborationOriginator(r, actorType, actorID)
	if !h.canInvokeAgent(r.Context(), target, actorType, actorID, originator, uuidToString(workspaceID)) {
		writeError(w, http.StatusForbidden, "agent invocation is not allowed")
		return
	}
	sourceTaskID := h.collaborationSourceTask(r, actorType, actorID)
	if actorType == "agent" && !sourceTaskID.Valid {
		writeError(w, http.StatusForbidden, "an active source Run is required")
		return
	}
	fingerprint := collaborationFingerprint("next", req.ClientRequestID, uuidToString(base.ID), req.Content, actorType, actorID)
	fireAt := pgtype.Timestamptz{Time: time.Now().UTC().Add(500 * time.Millisecond), Valid: true}
	op, task, comment, replay, err := h.createCollaborationTask(r, collaborationTaskInput{
		Operation: "next", Fingerprint: fingerprint, ClientRequestID: req.ClientRequestID,
		ActorType: actorType, ActorID: actorUUID, SourceTaskID: sourceTaskID,
		Issue: issue, Target: target, Content: req.Content, ForceFresh: false,
		Originator: originator, FireAt: fireAt,
	})
	if err != nil {
		h.writeCollaborationError(w, err)
		return
	}
	if !replay {
		h.TaskService.NotifyTaskEnqueued(r.Context(), task)
		h.publishCollaborationComment(issue, actorType, actorUUID, comment)
	}
	writeJSON(w, http.StatusAccepted, collaborationOperationResponse(op, task, comment))
}
