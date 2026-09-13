package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// Mirror event taxonomy persisted to runtime_mirror_event. Kept in sync with
// the CHECK constraint in migration 458.
const (
	mirrorEventSessionStarted  = "session_started"
	mirrorEventSessionAnswered = "session_answered"
	mirrorEventSessionFailed   = "session_failed"
	mirrorEventViewerStarted   = "viewer_started"
	mirrorEventViewerStopped   = "viewer_stopped"
)

const mirrorEventsDefaultLimit = 100
const mirrorEventsMaxLimit = 500

type mirrorEventRow struct {
	ID            string    `json:"id"`
	RuntimeID     string    `json:"runtime_id"`
	RuntimeName   string    `json:"runtime_name"`
	Event         string    `json:"event"`
	FailureReason string    `json:"failure_reason,omitempty"`
	ViewerID      string    `json:"viewer_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// recordMirrorEvent is best-effort: event logging must never break a live
// mirror session or its control flow.
func (h *Handler) recordMirrorEvent(ctx context.Context, workspaceID pgtype.UUID, runtimeID pgtype.UUID, runtimeName, event, failureReason, viewerID string) {
	if h.DB == nil {
		return
	}
	_, err := h.DB.Exec(ctx, `
		INSERT INTO runtime_mirror_event
			(id, workspace_id, runtime_id, runtime_name, event, failure_reason, viewer_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		dbid.NewV7(), workspaceID, runtimeID, truncateForLog(runtimeName, 200),
		event, truncateForLog(failureReason, 200), truncateForLog(viewerID, 128),
	)
	if err != nil {
		slog.Warn("mirror event write failed", "event", event, "error", err)
	}
}

func truncateForLog(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

type mirrorEventsResponse struct {
	Events []mirrorEventRow `json:"events"`
}

// ListMirrorEvents returns the most recent mirror events for the workspace.
func (h *Handler) ListMirrorEvents(w http.ResponseWriter, r *http.Request) {
	idUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	limit := mirrorEventsDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > mirrorEventsMaxLimit {
		limit = mirrorEventsMaxLimit
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT id, runtime_id, runtime_name, event, failure_reason, viewer_id, created_at
		FROM runtime_mirror_event
		WHERE workspace_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2`, idUUID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load mirror events")
		return
	}
	defer rows.Close()

	events := make([]mirrorEventRow, 0, limit)
	for rows.Next() {
		var (
			id, runtimeID                               pgtype.UUID
			runtimeName, event, failureReason, viewerID string
			createdAt                                   time.Time
		)
		if err := rows.Scan(&id, &runtimeID, &runtimeName, &event, &failureReason, &viewerID, &createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read mirror events")
			return
		}
		events = append(events, mirrorEventRow{
			ID:            util.UUIDToString(id),
			RuntimeID:     util.UUIDToString(runtimeID),
			RuntimeName:   runtimeName,
			Event:         event,
			FailureReason: failureReason,
			ViewerID:      viewerID,
			CreatedAt:     createdAt,
		})
	}
	writeJSON(w, http.StatusOK, mirrorEventsResponse{Events: events})
}

// ClearMirrorEvents deletes every mirror event for the workspace.
func (h *Handler) ClearMirrorEvents(w http.ResponseWriter, r *http.Request) {
	idUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	if _, err := h.DB.Exec(r.Context(),
		`DELETE FROM runtime_mirror_event WHERE workspace_id = $1`, idUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear mirror events")
		return
	}
	writeJSON(w, http.StatusOK, mirrorEventsResponse{Events: []mirrorEventRow{}})
}

// recordMirrorEventForRuntime is used on the daemon ingress path where only
// string IDs are available. It loads the runtime to resolve its display name;
// a lookup failure must never interrupt the answer/failure control flow.
func (h *Handler) recordMirrorEventForRuntime(ctx context.Context, workspaceID, runtimeID, event, failureReason, viewerID string) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return
	}
	rtUUID, err := util.ParseUUID(runtimeID)
	if err != nil {
		return
	}
	rt, err := h.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
		ID:          rtUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		return
	}
	h.recordMirrorEvent(ctx, wsUUID, rtUUID, mirrorRuntimeNoticeName(rt), event, failureReason, viewerID)
}
