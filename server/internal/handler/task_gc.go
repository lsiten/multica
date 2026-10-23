package handler

import (
	"encoding/json"
	"fmt"
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
	issueIDs := make([]pgtype.UUID, 0, len(rows))
	seenIssues := make(map[pgtype.UUID]bool, len(rows))
	for _, row := range rows {
		if row.IssueID.Valid && !row.ChatSessionID.Valid && !seenIssues[row.IssueID] {
			seenIssues[row.IssueID] = true
			issueIDs = append(issueIDs, row.IssueID)
		}
	}
	families := make(map[pgtype.UUID]db.ListCodeWorkspaceRetentionsRow, len(issueIDs))
	if len(issueIDs) > 0 {
		facts, err := h.Queries.ListCodeWorkspaceRetentions(r.Context(), db.ListCodeWorkspaceRetentionsParams{WorkspaceID: workspaceID, IssueIds: issueIDs})
		if err != nil {
			return nil, fmt.Errorf("query code workspace retention: %w", err)
		}
		for _, fact := range facts {
			families[fact.IssueID] = fact
		}
	}
	results := make([]protocol.TaskGCStatus, 0, len(rows))
	for _, row := range rows {
		result := protocol.TaskGCStatus{
			RunKind: inventoryRunKind(row.Context, row.IssueID, row.ChatSessionID, row.AutopilotRunID), ProjectID: uuidToString(row.ProjectID),
			TaskID: uuidToString(row.ID), WorkspaceID: uuidToString(row.WorkspaceID), RuntimeID: uuidToString(row.RuntimeID), AgentID: uuidToString(row.AgentID),
			WorkDir: row.WorkDir.String, Status: row.Status, CompletedAt: row.CompletedAt.Time, LifecycleSupported: true, RetentionSupported: true,
			IssueID: uuidToString(row.IssueID), ChatSessionID: uuidToString(row.ChatSessionID), AutopilotRunID: uuidToString(row.AutopilotRunID), AutopilotID: uuidToString(row.AutopilotID),
			IssueStatus: row.IssueStatus.String, IssueRevision: row.IssueRevision.Int64, CurrentWorkDir: row.CurrentWorkDir, CurrentTaskID: row.CurrentTaskID,
			WaitingHuman: row.WaitingHuman, CurrentAgent: row.CurrentAgent, ChatStatus: row.ChatStatus.String,
		}
		if row.IssueID.Valid && !row.ChatSessionID.Valid {
			family, known := families[row.IssueID]
			if known {
				result.CodeWorkspaceIssueID = uuidToString(family.RootIssueID)
				result.CodeWorkspaceRetentionBlocked = family.RetentionBlocked
			} else {
				result.RetentionSupported = false
			}
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
