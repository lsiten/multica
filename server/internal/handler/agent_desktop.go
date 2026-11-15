package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) GetAgentDesktopReadiness(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "agent_desktop_readiness", chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	if !rt.DaemonID.Valid || rt.Status != "online" {
		writeMirrorReason(w, http.StatusServiceUnavailable, "daemon_unavailable")
		return
	}
	readiness, err := h.DaemonHub.QueryAgentDesktopReadiness(
		r.Context(),
		uuidToString(rt.WorkspaceID),
		uuidToString(rt.ID),
		rt.DaemonID.String,
	)
	if err != nil {
		writeMirrorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, readiness)
}

func (h *Handler) OpenAgentDesktopPermissionGuide(w http.ResponseWriter, r *http.Request) {
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "agent_desktop_permission_guide", chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	if !canEditRuntime(member, rt) {
		writeError(w, http.StatusForbidden, "only runtime owners and workspace admins can open the permission guide")
		return
	}
	if !rt.DaemonID.Valid || rt.Status != "online" {
		writeMirrorReason(w, http.StatusServiceUnavailable, "daemon_unavailable")
		return
	}
	readiness, err := h.DaemonHub.OpenAgentDesktopPermissionGuide(
		r.Context(),
		uuidToString(rt.WorkspaceID),
		uuidToString(rt.ID),
		rt.DaemonID.String,
	)
	if err != nil {
		writeMirrorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, readiness)
}
