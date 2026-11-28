package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	generated "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) ensureCollaborationProjectScope(ctx context.Context, issue generated.Issue, target generated.Agent) error {
	if !issue.ProjectID.Valid {
		return nil
	}
	allowed, err := h.Queries.AgentMayUseProject(ctx, generated.AgentMayUseProjectParams{LeadID: target.ID, ID: issue.ProjectID})
	if err != nil {
		return err
	}
	if !allowed.Valid || !allowed.Bool {
		return errors.New("target agent is not available for this project")
	}
	return nil
}

func collaborationTrace(ctx context.Context, operation string) func() {
	started := time.Now()
	return func() {
		slog.DebugContext(ctx, "agent collaboration operation", "operation", operation, "elapsed_ms", time.Since(started).Milliseconds())
	}
}

func (h *Handler) GetAgentCollaborationOperation(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "operationId"), "operation id")
	if !ok {
		return
	}
	workspace, ok := parseUUIDOrBadRequest(w, ctxWorkspaceID(r.Context()), "workspace id")
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, requestUserID(r), uuidToString(workspace))
	actor, ok := parseUUIDOrBadRequest(w, actorID, "actor id")
	if !ok {
		return
	}
	op, err := h.Queries.GetAgentCollaborationOperation(r.Context(), id)
	if err != nil || op.WorkspaceID != workspace || op.ActorType != actorType || op.ActorID != actor {
		writeError(w, http.StatusNotFound, "collaboration operation not found")
		return
	}
	if !h.requireIssueIDVisible(w, r, uuidToString(workspace), op.IssueID, http.StatusNotFound, "collaboration operation not found") {
		return
	}
	writeJSON(w, http.StatusOK, collaborationOperationResponse(op, generated.AgentTaskQueue{}, generated.CreateCommentRow{}))
}

func (h *Handler) failCollaborationOperation(ctx context.Context, id pgtype.UUID, message string) {
	_, _ = h.Queries.CompleteAgentCollaborationOperation(ctx, generated.CompleteAgentCollaborationOperationParams{ID: id, Status: "failed", Error: pgtype.Text{String: message, Valid: true}})
}

func collaborationOperationResponse(op generated.AgentCollaborationOperation, task generated.AgentTaskQueue, comment generated.CreateCommentRow) protocol.AgentCollaborationOperation {
	result := map[string]any{}
	if len(op.Result) > 0 {
		_ = json.Unmarshal(op.Result, &result)
	}
	return protocol.AgentCollaborationOperation{ID: uuidToString(op.ID), Operation: op.Operation, Status: op.Status, SourceTaskID: uuidToString(op.SourceTaskID), TargetAgentID: uuidToString(op.TargetAgentID), TargetTaskID: uuidToString(op.TargetTaskID), CommentID: uuidToString(op.CommentID), Result: result, Error: op.Error.String}
}

func decodeAgentCollaborationCommand(w http.ResponseWriter, r *http.Request, out *agentCollaborationCommand) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, collaborationOperationMaxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, "invalid collaboration request")
		return false
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid collaboration request")
		return false
	}
	return true
}
func collaborationFingerprint(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte{0})
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func collaborationPriority(priority string) int32 {
	switch priority {
	case "urgent":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}
func collaborationTruncate(content string, max int) string {
	runes := []rune(strings.Join(strings.Fields(content), " "))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}
func (h *Handler) collaborationOriginator(r *http.Request, actorType, actorID string) string {
	if actorType == "member" {
		return actorID
	}
	return h.invokeOriginatorFromRequest(r, actorType, actorID)
}
func (h *Handler) collaborationSourceTask(r *http.Request, actorType, actorID string) pgtype.UUID {
	if actorType != "agent" {
		return pgtype.UUID{}
	}
	return h.taskSourceOrEmpty(r, actorType, actorID)
}

func (h *Handler) taskSourceOrEmpty(r *http.Request, actorType, actorID string) pgtype.UUID {
	if actorType != "agent" {
		return pgtype.UUID{}
	}
	if task, ok := h.taskFromRequestHeader(r); ok && uuidToString(task.AgentID) == actorID && (task.Status == "dispatched" || task.Status == "running" || task.Status == "waiting_local_directory") {
		return task.ID
	}
	return pgtype.UUID{}
}
func (h *Handler) writeCollaborationError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if strings.Contains(err.Error(), "different content") {
		status = http.StatusConflict
	}
	writeError(w, status, err.Error())
}

func (h *Handler) publishCollaborationComment(issue generated.Issue, actorType string, actorID pgtype.UUID, comment generated.CreateCommentRow) {
	resp := commentToResponse(comment.Comment(), nil, nil)
	h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), actorType, uuidToString(actorID), map[string]any{
		"comment":             resp,
		"issue_title":         issue.Title,
		"issue_assignee_type": textToPtr(issue.AssigneeType),
		"issue_assignee_id":   uuidToPtr(issue.AssigneeID),
		"issue_status":        issue.Status,
		"issue_revision":      comment.IssueRevision,
	})
}
