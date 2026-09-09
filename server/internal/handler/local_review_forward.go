package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ForwardLocalReview authorizes from existing task metadata, then relays the
// runtime response without inserting a review, command, event, or snapshot row.
func (h *Handler) ForwardLocalReview(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "human actor required")
		return
	}
	var input struct {
		TaskID     string `json:"task_id"`
		Path       string `json:"path"`
		Target     string `json:"target"`
		Action     string `json:"action"`
		SnapshotID string `json:"snapshot_id"`
		VersionID  string `json:"version_id,omitempty"`
		FilePath   string `json:"file_path,omitempty"`
		Side       string `json:"side,omitempty"`
		Offset     int    `json:"offset,omitempty"`
		Limit      int    `json:"limit,omitempty"`
		Comment    string `json:"comment"`
		CommandID  string `json:"command_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&input) != nil || (input.Action != "branches" && input.Action != "repositories" && strings.TrimSpace(input.Target) == "") || len(input.Target) > 250 {
		writeError(w, http.StatusBadRequest, "task and target branch required")
		return
	}
	switch input.Action {
	case "", "read":
		input.Action = "read"
	case "branches":
	case "repositories", "manifest", "files", "file", "context", "content", "commits", "lease":
		maxLimit := 500
		if input.Action == "content" {
			maxLimit = 64 << 10
		}
		if len(input.VersionID) > 64 || len(input.FilePath) > 4096 || input.Offset < 0 || input.Limit < 0 || input.Limit > maxLimit || (input.Action != "manifest" && input.Action != "repositories" && input.VersionID == "") || (input.Side != "" && input.Side != "old" && input.Side != "new") {
			writeError(w, http.StatusBadRequest, "invalid review page request")
			return
		}
	case "submit", "approve", "request_changes", "merge":
		if strings.TrimSpace(input.CommandID) == "" || len(input.CommandID) > 128 {
			writeError(w, http.StatusBadRequest, "valid operation ID required")
			return
		}
		if input.SnapshotID == "" || len(input.SnapshotID) > 128 || len(input.Comment) > 8000 {
			writeError(w, http.StatusBadRequest, "reviewed snapshot and bounded comment required")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "unknown review action")
		return
	}
	id, ok := parseUUIDOrBadRequest(w, input.TaskID, "task_id")
	if !ok {
		return
	}
	task, err := h.Queries.GetAgentTaskInWorkspace(r.Context(), db.GetAgentTaskInWorkspaceParams{ID: id, WorkspaceID: parseUUID(ctxWorkspaceID(r.Context()))})
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	runtime, _, ok := h.requireRuntimeReadAccess(w, r, obsmetrics.RuntimeLookupSourceRuntimeAPI, uuidToString(task.RuntimeID))
	if !ok {
		return
	}
	if task.IssueID.Valid {
		if _, ok := h.loadIssueForUser(w, r, uuidToString(task.IssueID)); !ok {
			return
		}
	} else if uuidToString(runtime.OwnerID) != user {
		writeError(w, http.StatusForbidden, "only the runtime owner can review this run")
		return
	}
	if input.Action == "merge" && uuidToString(runtime.OwnerID) != user {
		writeError(w, http.StatusForbidden, "only the runtime owner can merge")
		return
	}
	path := task.WorkDir.String
	if path == "" {
		writeError(w, http.StatusConflict, "task has not reported a worktree")
		return
	}
	if input.Path != "" {
		base := strings.TrimRight(strings.ReplaceAll(path, "\\", "/"), "/")
		selected := strings.ReplaceAll(input.Path, "\\", "/")
		if selected != base && !strings.HasPrefix(selected, base+"/") {
			writeError(w, http.StatusBadRequest, "repository is outside task worktree")
			return
		}
		path = input.Path
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	exchange, err := h.localReviewRelay.enqueue(protocol.LocalReviewCommand{
		WorkspaceID: ctxWorkspaceID(r.Context()), RuntimeID: uuidToString(task.RuntimeID),
		TaskID: input.TaskID, Path: path, Target: input.Target, Action: input.Action,
		SnapshotID: input.SnapshotID, Comment: input.Comment, ActorID: user,
		CommandID: input.CommandID,
		VersionID: input.VersionID, FilePath: input.FilePath, Offset: input.Offset, Limit: input.Limit,
		Side: input.Side,
	})
	if err != nil {
		writeError(w, http.StatusTooManyRequests, "local review relay busy")
		return
	}
	result, err := h.localReviewRelay.wait(ctx, exchange)
	if err != nil {
		writeError(w, http.StatusGatewayTimeout, "runtime did not respond; no cloud snapshot is available")
		return
	}
	if result.Error != "" {
		writeError(w, http.StatusConflict, result.Error)
		return
	}
	result.ClaimToken = ""
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) ClaimLocalReviewRelay(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.localReviewRuntime(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, protocol.LocalReviewClaim{Command: h.localReviewRelay.claim(uuidToString(runtime.WorkspaceID), uuidToString(runtime.ID))})
}

func (h *Handler) ReportLocalReviewRelay(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.localReviewRuntime(w, r)
	if !ok {
		return
	}
	var result protocol.LocalReviewResult
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 12<<20)).Decode(&result) != nil || len(result.Error) > 8000 {
		writeError(w, http.StatusBadRequest, "invalid review result")
		return
	}
	if !h.localReviewRelay.complete(uuidToString(runtime.WorkspaceID), uuidToString(runtime.ID), chi.URLParam(r, "commandId"), result) {
		writeError(w, http.StatusGone, "review request expired or is not owned by runtime")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}
