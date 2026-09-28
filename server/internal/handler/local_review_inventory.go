package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type localReviewWorktreeResponse struct {
	protocol.WorktreeLifecycle
	TaskID      pgtype.UUID `json:"task_id"`
	RuntimeID   pgtype.UUID `json:"runtime_id"`
	AgentID     pgtype.UUID `json:"agent_id"`
	IssueID     pgtype.UUID `json:"issue_id"`
	WorkDir     pgtype.Text `json:"work_dir"`
	Status      string      `json:"status"`
	BranchName  pgtype.Text `json:"branch_name"`
	WorkspaceID pgtype.UUID `json:"workspace_id"`
}

func (h *Handler) localReviewRuntime(w http.ResponseWriter, r *http.Request) (db.AgentRuntime, bool) {
	return h.localReviewRuntimeForID(w, r, chi.URLParam(r, "runtimeId"))
}

func (h *Handler) localReviewRuntimeForID(w http.ResponseWriter, r *http.Request, runtimeID string) (db.AgentRuntime, bool) {
	runtime, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return runtime, false
	}
	daemonID := middleware.DaemonIDFromContext(r.Context())
	if daemonID != "" {
		if runtime.DaemonID.Valid && runtime.DaemonID.String == daemonID {
			return runtime, true
		}
	} else if runtime.OwnerID.Valid && uuidToString(runtime.OwnerID) == r.Header.Get("X-User-ID") {
		return runtime, true
	}
	writeError(w, http.StatusForbidden, "runtime owner credential required")
	return runtime, false
}

// ListLocalReviewWorktrees uses existing task metadata, not a cloud MR index.
func (h *Handler) ListLocalReviewWorktrees(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	params := db.ListLocalReviewWorktreesParams{WorkspaceID: parseUUID(ctxWorkspaceID(r.Context())), ReaderID: parseUUID(user)}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || offset < 0 {
			writeError(w, 400, "invalid offset")
			return
		}
		params.PageOffset = int32(offset)
	}
	if raw := r.URL.Query().Get("agent_id"); raw != "" {
		id, valid := parseUUIDOrBadRequest(w, raw, "agent_id")
		if !valid {
			return
		}
		params.AgentID = id
	}
	if raw := r.URL.Query().Get("issue_id"); raw != "" {
		id, valid := parseUUIDOrBadRequest(w, raw, "issue_id")
		if !valid {
			return
		}
		params.IssueID = id
	}
	rows, err := h.Queries.ListLocalReviewWorktrees(r.Context(), params)
	if err != nil {
		writeError(w, 500, "cannot list runtime worktrees")
		return
	}
	result := make([]localReviewWorktreeResponse, 0, len(rows))
	resolver := issuestatus.NewResolver(params.WorkspaceID)
	for _, row := range rows {
		lifecycle := protocol.WorktreeLifecycle{RunStatus: row.Status, NextAction: protocol.WorktreeUnknown, RepositoriesDetails: []protocol.WorktreeRepositoryLifecycle{}}
		switch row.Status {
		case "queued", "dispatched", "running", "waiting_local_directory", "deferred":
			lifecycle.NextAction = protocol.WorktreeActive
		}
		if row.IssueID.Valid {
			lifecycle.IssueID = uuidToString(row.IssueID)
			lifecycle.IssueStatus = row.IssueStatus.String
			lifecycle.IssueStatusCategory = resolver.Category(r.Context(), h.issueStatusCatalog(), row.IssueStatus.String)
		}
		if row.CompletedAt.Valid {
			lifecycle.CompletedAt = &row.CompletedAt.Time
		}
		if row.LastActivityAt.Valid {
			lifecycle.LastActivityAt = &row.LastActivityAt.Time
		}
		lifecycle.Stale = protocol.WorktreeStale(lifecycle, time.Now(), protocol.DefaultWorktreeStaleTTL)
		result = append(result, localReviewWorktreeResponse{WorktreeLifecycle: lifecycle, TaskID: row.TaskID, RuntimeID: row.RuntimeID, AgentID: row.AgentID, IssueID: row.IssueID, WorkDir: row.WorkDir, Status: row.Status, BranchName: row.BranchName, WorkspaceID: row.WorkspaceID})
	}
	writeJSON(w, 200, result)
}
