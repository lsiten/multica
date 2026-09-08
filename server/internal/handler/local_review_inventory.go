package handler

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

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
	rows, err := h.Queries.ListLocalReviewWorktrees(r.Context(), params)
	if err != nil {
		writeError(w, 500, "cannot list runtime worktrees")
		return
	}
	writeJSON(w, 200, rows)
}
