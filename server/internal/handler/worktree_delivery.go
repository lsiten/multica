package handler

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func validWorktreeCommit(commit string) bool {
	if len(commit) != 40 && len(commit) != 64 {
		return false
	}
	_, err := hex.DecodeString(commit)
	return err == nil
}

// RecordWorktreeDelivery accepts the final receipt after every shared writer exits.
func (h *Handler) RecordWorktreeDelivery(w http.ResponseWriter, r *http.Request) {
	task, workspaceID, ok := h.requireDaemonTaskAccessWithWorkspace(w, r, chi.URLParam(r, "taskId"))
	if !ok {
		return
	}
	var req struct {
		BranchName string `json:"branch_name"`
		Commit     string `json:"worktree_commit"`
		WorkDir    string `json:"work_dir"`
		NoWork     bool   `json:"no_work"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	valid := validWorktreeCommit(req.Commit) && req.BranchName != "" && !strings.HasPrefix(req.BranchName, "-")
	if req.NoWork {
		valid = req.Commit == "" && req.BranchName == ""
	}
	if !valid || req.WorkDir == "" {
		writeError(w, http.StatusBadRequest, "invalid shared worktree delivery")
		return
	}
	updated, err := h.Queries.RecordSharedWorktreeDelivery(r.Context(), db.RecordSharedWorktreeDeliveryParams{ID: task.ID, RuntimeID: task.RuntimeID, WorkspaceID: parseUUID(workspaceID), WorkDir: req.WorkDir, BranchName: req.BranchName, Commit: req.Commit, NoWork: req.NoWork})
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusConflict, "worktree delivery scope changed")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to record worktree delivery")
		return
	}
	h.TaskService.NotifyWorktreeDelivery(updated, workspaceID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
