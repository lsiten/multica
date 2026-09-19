package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/daemonws"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) requireVscreenAccess(w http.ResponseWriter, r *http.Request) (db.AgentRuntime, db.Member, bool) {
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "vscreen", chi.URLParam(r, "runtimeId"))
	if !ok {
		return rt, member, false
	}
	if !runtimeHasCapability(rt.Metadata, protocol.DaemonCapabilityVirtualScreenV1) || !runtimeHasCapability(rt.Metadata, protocol.DaemonCapabilityMirrorViewerGrantV1) {
		writeVscreenReason(w, http.StatusNotImplemented, "upgrade_required")
		return rt, member, false
	}
	if !rt.DaemonID.Valid || rt.Status != "online" || h.DaemonHub == nil {
		writeVscreenReason(w, http.StatusServiceUnavailable, "daemon_unavailable")
		return rt, member, false
	}
	return rt, member, true
}

// GetVscreen retrieves a fresh authenticated native observation.
func (h *Handler) GetVscreen(w http.ResponseWriter, r *http.Request) {
	h.getVscreenObservation(w, r, "state")
}

// GetMirrorSources lists only this runtime's authorized source bindings.
func (h *Handler) GetMirrorSources(w http.ResponseWriter, r *http.Request) {
	h.getVscreenObservation(w, r, "sources")
}

func (h *Handler) getVscreenObservation(w http.ResponseWriter, r *http.Request, kind string) {
	rt, _, ok := h.requireVscreenAccess(w, r)
	if !ok {
		return
	}
	result, err := h.DaemonHub.QueryVscreen(r.Context(), uuidToString(rt.WorkspaceID), uuidToString(rt.ID), rt.DaemonID.String, kind)
	if err != nil {
		writeVscreenError(w, err)
		return
	}
	if result.Reason != "" {
		writeVscreenReason(w, http.StatusConflict, string(result.Reason))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateVscreenCommand accepts only remote-safe owner commands.
func (h *Handler) CreateVscreenCommand(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "vscreen_command", runtimeID)
	if !ok {
		return
	}
	if isMachineCredentialActor(r) || !canSetRuntimeVisibility(member, rt) {
		writeVscreenReason(w, http.StatusForbidden, "permission_denied")
		return
	}
	var req struct {
		CommandID string                      `json:"command_id"`
		Kind      protocol.VscreenCommandKind `json:"kind"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(json.RawMessage)) != io.EOF {
		writeVscreenReason(w, http.StatusBadRequest, "invalid_command")
		return
	}
	if req.Kind.HostInteractionCommand() {
		if !rt.DaemonID.Valid || rt.Status != "online" || h.DaemonHub == nil {
			writeVscreenReason(w, http.StatusServiceUnavailable, "daemon_unavailable")
			return
		}
		if !runtimeHasCapability(rt.Metadata, protocol.DaemonCapabilityScreenControlV1) {
			writeVscreenReason(w, http.StatusNotImplemented, "upgrade_required")
			return
		}
	} else if !runtimeHasCapability(rt.Metadata, protocol.DaemonCapabilityVirtualScreenV1) ||
		!runtimeHasCapability(rt.Metadata, protocol.DaemonCapabilityMirrorViewerGrantV1) ||
		!rt.DaemonID.Valid || rt.Status != "online" || h.DaemonHub == nil {
		writeVscreenReason(w, http.StatusServiceUnavailable, "daemon_unavailable")
		return
	}
	receipt, err := h.DaemonHub.SubmitVscreenCommand(uuidToString(rt.WorkspaceID), uuidToString(rt.ID), rt.DaemonID.String, requestUserID(r), req.CommandID, req.Kind)
	if err != nil {
		writeVscreenError(w, err)
		return
	}
	if req.Kind == protocol.VscreenCommandDisableInteraction || req.Kind == protocol.VscreenCommandEmergencyStop {
		h.revokeRuntimeControlGrants(rt)
	}
	writeJSON(w, http.StatusAccepted, receipt)
}

// GetVscreenCommand exposes a receipt without replaying its command.
func (h *Handler) GetVscreenCommand(w http.ResponseWriter, r *http.Request) {
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "vscreen_command", chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	receipt, err := h.DaemonHub.VscreenCommandStatus(uuidToString(rt.WorkspaceID), uuidToString(rt.ID), rt.DaemonID.String, requestUserID(r), chi.URLParam(r, "commandId"), canSetRuntimeVisibility(member, rt))
	if err != nil {
		writeVscreenError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func writeVscreenReason(w http.ResponseWriter, status int, reason string) {
	writeJSON(w, status, struct {
		Reason string `json:"reason"`
	}{reason})
}
func writeVscreenError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, daemonws.ErrVscreenNotFound):
		writeVscreenReason(w, http.StatusNotFound, "command_not_found")
	case errors.Is(err, daemonws.ErrVscreenStale):
		writeVscreenReason(w, http.StatusConflict, "stale_generation")
	case errors.Is(err, daemonws.ErrVscreenReplay):
		writeVscreenReason(w, http.StatusConflict, "command_replayed")
	case errors.Is(err, protocol.ErrInvalidVscreenContract):
		writeVscreenReason(w, http.StatusBadRequest, "invalid_command")
	case errors.Is(err, context.DeadlineExceeded):
		writeVscreenReason(w, http.StatusGatewayTimeout, "daemon_timeout")
	default:
		writeVscreenReason(w, http.StatusServiceUnavailable, "daemon_unavailable")
	}
}
