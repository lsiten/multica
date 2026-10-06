package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// GetTaskGCCheck supplies run and issue facts without granting cleanup or Git access.
func (h *Handler) GetTaskGCCheck(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskId"), "task_id")
	if !ok {
		return
	}
	task, err := h.Queries.GetAgentTask(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "task not found")
		} else {
			writeError(w, http.StatusServiceUnavailable, "task lifecycle unavailable")
		}
		return
	}
	agent, err := h.Queries.GetAgent(r.Context(), task.AgentID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "task owner unavailable")
		return
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(agent.WorkspaceID)) {
		return
	}
	rows, err := h.taskGCLifecycles(r, agent.WorkspaceID, task.RuntimeID, []pgtype.UUID{task.ID})
	if err != nil || len(rows) != 1 {
		writeError(w, http.StatusServiceUnavailable, "task lifecycle unavailable")
		return
	}
	writeJSON(w, http.StatusOK, rows[0])
}

// BatchTaskGCCheck returns current workline facts with bounded, runtime-scoped input.
func (h *Handler) BatchTaskGCCheck(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "workspaceId"), "workspace_id")
	if !ok {
		return
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(workspaceID)) {
		return
	}
	var runtimeID pgtype.UUID
	if raw := chi.URLParam(r, "runtimeId"); raw != "" {
		runtimeID, ok = parseUUIDOrBadRequest(w, raw, "runtime_id")
		if !ok {
			return
		}
	}
	var request struct {
		TaskIDs []string `json:"task_ids"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || len(request.TaskIDs) == 0 || len(request.TaskIDs) > 500 {
		writeError(w, http.StatusBadRequest, "expected 1-500 task_ids")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "single request required")
		return
	}
	ids := make([]pgtype.UUID, 0, len(request.TaskIDs))
	seen := make(map[pgtype.UUID]bool, len(request.TaskIDs))
	for _, id := range request.TaskIDs {
		parsed, ok := parseUUIDOrBadRequest(w, id, "task_id")
		if !ok {
			return
		}
		if !seen[parsed] {
			seen[parsed] = true
			ids = append(ids, parsed)
		}
	}
	rows, err := h.taskGCLifecycles(r, workspaceID, runtimeID, ids)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "task lifecycle unavailable")
		return
	}
	missing, err := h.Queries.ListMissingTaskGCIDs(r.Context(), ids)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "task lifecycle unavailable")
		return
	}
	for _, id := range missing {
		rows = append(rows, protocol.TaskGCStatus{TaskID: uuidToString(id), WorkspaceID: uuidToString(workspaceID), RuntimeID: uuidToString(runtimeID), Missing: true, LifecycleSupported: true, RetentionSupported: true})
	}
	writeJSON(w, http.StatusOK, protocol.TaskGCBatch{Tasks: rows})
}

func (h *Handler) taskGCLifecycles(r *http.Request, workspaceID, runtimeID pgtype.UUID, ids []pgtype.UUID) ([]protocol.TaskGCStatus, error) {
	rows, err := h.Queries.ListTaskGCLifecycles(r.Context(), db.ListTaskGCLifecyclesParams{WorkspaceID: workspaceID, RuntimeID: runtimeID, TaskIds: ids})
	if err != nil {
		slog.Warn("task GC lifecycle query failed", "error", err)
		return nil, err
	}
	resolver := issuestatus.NewResolver(workspaceID)
	results := make([]protocol.TaskGCStatus, 0, len(rows))
	for _, row := range rows {
		result := protocol.TaskGCStatus{
			TaskID: uuidToString(row.ID), WorkspaceID: uuidToString(row.WorkspaceID), RuntimeID: uuidToString(row.RuntimeID), AgentID: uuidToString(row.AgentID),
			WorkDir: row.WorkDir.String, Status: row.Status, CompletedAt: row.CompletedAt.Time, LifecycleSupported: true, RetentionSupported: true,
			IssueID: uuidToString(row.IssueID), ChatSessionID: uuidToString(row.ChatSessionID), AutopilotRunID: uuidToString(row.AutopilotRunID), AutopilotID: uuidToString(row.AutopilotID),
			IssueStatus: row.IssueStatus.String, IssueRevision: row.IssueRevision.Int64, CurrentWorkDir: row.CurrentWorkDir, CurrentTaskID: row.CurrentTaskID,
			WaitingHuman: row.WaitingHuman, CurrentAgent: row.CurrentAgent, ChatStatus: row.ChatStatus.String,
		}
		if row.IssueStatus.Valid {
			result.IssueStatusCategory = resolver.Category(r.Context(), h.issueStatusCatalog(), row.IssueStatus.String)
			if !issuestatus.IsCategory(result.IssueStatusCategory) {
				result.RetentionSupported = false
			}
		}
		if row.LastActivityAt.Valid {
			result.LastActivityAt = &row.LastActivityAt.Time
		}
		if row.PrepareLeaseExpiresAt.Valid {
			result.PrepareLeaseExpiresAt = &row.PrepareLeaseExpiresAt.Time
		}
		results = append(results, result)
	}
	return results, nil
}
