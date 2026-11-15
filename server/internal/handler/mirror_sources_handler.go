package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// GetMirrorSources returns the current daemon's authorized, read-only display
// catalog. The catalog contains no ability to send pointer, keyboard, window,
// or takeover commands.
func (h *Handler) GetMirrorSources(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "mirror_sources", chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	if !rt.DaemonID.Valid || rt.Status != "online" {
		writeMirrorReason(w, http.StatusServiceUnavailable, "daemon_unavailable")
		return
	}
	result, err := h.DaemonHub.QueryMirrorSources(
		r.Context(),
		uuidToString(rt.WorkspaceID),
		uuidToString(rt.ID),
		rt.DaemonID.String,
	)
	if err != nil {
		writeMirrorError(w, err)
		return
	}
	if result.Reason != "" {
		writeMirrorReason(w, http.StatusConflict, string(result.Reason))
		return
	}
	writeJSON(w, http.StatusOK, result)
}
