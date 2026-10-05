package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
)

// GetIssueProgress inspects recorded descendants without changing issue state.
func (h *Handler) GetIssueProgress(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	h.writeIssueProgress(w, r, issue.WorkspaceID, service.ProgressScope{Type: "issue", ID: uuidToString(issue.ID)})
}

// GetProjectAttention inspects project work independently of supervision enablement.
func (h *Handler) GetProjectAttention(w http.ResponseWriter, r *http.Request) {
	project, _, ok := h.loadProjectScopeBindingTarget(w, r)
	if !ok {
		return
	}
	h.writeIssueProgress(w, r, project.WorkspaceID, service.ProgressScope{Type: "project", ID: uuidToString(project.ID)})
}

func (h *Handler) writeIssueProgress(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID, scope service.ProgressScope) {
	if r.Header.Get("X-Actor-Source") == "task_token" {
		writeError(w, http.StatusForbidden, "progress inspection requires a member")
		return
	}
	member, ok := h.workspaceMember(w, r, uuidToString(workspaceID))
	if !ok {
		return
	}
	query := r.URL.Query()
	options := service.ProgressOptions{Scope: scope, MemberID: uuidToString(member.UserID), Filter: query.Get("filter"), AssigneeType: query.Get("assignee_type"), AssigneeID: query.Get("assignee_id"), Cursor: query.Get("cursor")}
	if today := query.Get("today"); today != "" {
		if _, err := time.Parse("2006-01-02", today); err != nil {
			writeError(w, 400, "invalid calendar date")
			return
		}
		options.Today = today
	}
	switch options.Filter {
	case "", "all", "blocked", "review", "follow_up", "ready":
	default:
		writeError(w, 400, "invalid progress filter")
		return
	}
	switch options.AssigneeType {
	case "", "member", "agent", "squad":
	default:
		writeError(w, 400, "invalid assignee type")
		return
	}
	if options.AssigneeID != "" {
		if _, ok := parseUUIDOrBadRequest(w, options.AssigneeID, "assignee_id"); !ok {
			return
		}
		if options.AssigneeType == "" {
			writeError(w, 400, "assignee type is required")
			return
		}
	}
	if value := query.Get("mine"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			writeError(w, 400, "invalid mine filter")
			return
		}
		options.OnlyMine = parsed
	}
	if value := query.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 200 {
			writeError(w, 400, "progress limit must be between 1 and 200")
			return
		}
		options.Limit = limit
	}
	reader := service.IssueProgressService{TxStarter: h.TxStarter}
	view, err := reader.View(r.Context(), workspaceID, options)
	switch {
	case errors.Is(err, service.ErrProgressSnapshotChanged):
		writeJSON(w, 409, map[string]string{"code": "progress_snapshot_changed", "error": "progress changed; reload the first page"})
	case errors.Is(err, service.ErrProgressCursorInvalid):
		writeError(w, 400, "invalid progress cursor")
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, 404, "progress target not found")
	case err != nil:
		slog.Warn("progress inspection failed", "scope", scope.Type, "id", scope.ID, "error", err)
		writeError(w, 500, "failed to load progress")
	default:
		writeJSON(w, 200, view)
	}
}
