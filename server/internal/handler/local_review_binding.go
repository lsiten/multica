package handler

import (
	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"net/http"
)

// GetLocalReviewRuntimeBinding exposes only existing identity metadata to the
// owning daemon/user, allowing legacy local directories to acquire provenance.
func (h *Handler) GetLocalReviewRuntimeBinding(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.localReviewRuntime(w, r)
	if !ok {
		return
	}
	taskID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskId"), "task_id")
	if !ok {
		return
	}
	task, err := h.Queries.GetAgentTaskInWorkspace(r.Context(), db.GetAgentTaskInWorkspaceParams{ID: taskID, WorkspaceID: runtime.WorkspaceID})
	if err != nil || task.RuntimeID != runtime.ID {
		writeError(w, http.StatusNotFound, "task does not belong to this runtime")
		return
	}
	if directoryTaskID := r.URL.Query().Get("directory_task_id"); directoryTaskID != "" {
		ownerID, ok := parseUUIDOrBadRequest(w, directoryTaskID, "directory_task_id")
		if !ok {
			return
		}
		owner, err := h.Queries.GetAgentTaskInWorkspace(r.Context(), db.GetAgentTaskInWorkspaceParams{ID: ownerID, WorkspaceID: runtime.WorkspaceID})
		if err != nil || !reviewTasksShareDirectory(task, owner, r.URL.Query().Get("path")) {
			writeError(w, http.StatusForbidden, "directory reuse is not verified for this run")
			return
		}
		task = owner
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, protocol.LocalReviewRuntimeBinding{
		WorkspaceID: uuidToString(runtime.WorkspaceID), RuntimeID: uuidToString(runtime.ID), TaskID: uuidToString(task.ID), AgentID: uuidToString(task.AgentID),
	})
}

// DiscoverLocalReviewRuntimeBinding supports legacy inventory entries that know
// their task identity but not their runtime. Authorization uses the stored runtime.
func (h *Handler) DiscoverLocalReviewRuntimeBinding(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskId"), "task_id")
	if !ok {
		return
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskID)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "task not found")
		} else {
			writeError(w, http.StatusInternalServerError, "cannot load task identity")
		}
		return
	}
	runtime, ok := h.localReviewRuntimeForID(w, r, uuidToString(task.RuntimeID))
	if !ok {
		return
	}
	if _, err := h.Queries.GetAgentTaskInWorkspace(r.Context(), db.GetAgentTaskInWorkspaceParams{ID: taskID, WorkspaceID: runtime.WorkspaceID}); err != nil {
		writeError(w, http.StatusNotFound, "task not found in runtime workspace")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, protocol.LocalReviewRuntimeBinding{WorkspaceID: uuidToString(runtime.WorkspaceID), RuntimeID: uuidToString(runtime.ID), TaskID: uuidToString(task.ID), AgentID: uuidToString(task.AgentID)})
}
