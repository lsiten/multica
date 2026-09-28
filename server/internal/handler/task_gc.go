package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// GetTaskGCCheck supplies run and issue facts without granting cleanup or Git access.
func (h *Handler) GetTaskGCCheck(w http.ResponseWriter, r *http.Request) {
	task, workspace, ok := h.requireDaemonTaskAccessWithWorkspace(w, r, chi.URLParam(r, "taskId"))
	if !ok {
		return
	}
	result := protocol.TaskGCStatus{Status: task.Status, CompletedAt: task.CompletedAt.Time, LifecycleSupported: true}
	if task.IssueID.Valid {
		issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{ID: task.IssueID, WorkspaceID: parseUUID(workspace)})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "issue lifecycle unavailable")
			return
		}
		result.IssueID = uuidToString(issue.ID)
		result.IssueStatus = issue.Status
		result.IssueStatusCategory = issuestatus.Category(r.Context(), h.issueStatusCatalog(), issue.WorkspaceID, issue.Status)
		if issue.LastActivityAt.Valid {
			result.LastActivityAt = &issue.LastActivityAt.Time
		} else if issue.UpdatedAt.Valid {
			result.LastActivityAt = &issue.UpdatedAt.Time
		}
	}
	writeJSON(w, http.StatusOK, result)
}
