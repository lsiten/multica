package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var errMirrorControlDaemonUnavailable = errors.New("mirror control daemon unavailable")

type mirrorControlStatusResponse struct {
	State             string `json:"state"`
	ControlID         string `json:"control_id,omitempty"`
	RequestID         string `json:"request_id,omitempty"`
	ControllerID      string `json:"controller_id,omitempty"`
	RequesterID       string `json:"requester_id,omitempty"`
	LeaseExpiresAt    string `json:"lease_expires_at,omitempty"`
	LeaseMaxExpiresAt string `json:"lease_max_expires_at,omitempty"`
}

func (h *Handler) RequestAgentDesktopControl(w http.ResponseWriter, r *http.Request) {
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "agent_desktop_control_request", chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	userID := requestUserID(r)
	now := time.Now()
	if canEditRuntime(member, rt) {
		existing := h.mirrorControls.status(uuidToString(rt.ID), userID, true, now)
		if existing.State != "view_only" {
			writeJSON(w, http.StatusOK, mirrorControlResponse(existing))
			return
		}
		status, err := h.startAgentDesktopControl(r, rt, userID, now, nil)
		if err != nil {
			writeMirrorError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, status)
		return
	}
	status := h.mirrorControls.request(
		uuidToString(rt.ID),
		uuidToString(rt.WorkspaceID),
		userID,
		now,
	)
	writeJSON(w, http.StatusOK, mirrorControlResponse(status))
}

func (h *Handler) GetAgentDesktopControl(w http.ResponseWriter, r *http.Request) {
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "agent_desktop_control_status", chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	status := h.mirrorControls.status(
		uuidToString(rt.ID),
		requestUserID(r),
		canEditRuntime(member, rt),
		time.Now(),
	)
	if status.State == "ended" && status.Lease != nil {
		_, _ = h.DaemonHub.StopAgentDesktopControl(
			r.Context(),
			status.Lease.WorkspaceID,
			status.Lease.RuntimeID,
			status.Lease.DaemonID,
			status.Lease.ID,
		)
	}
	writeJSON(w, http.StatusOK, mirrorControlResponse(status))
}

func (h *Handler) ApproveAgentDesktopControl(w http.ResponseWriter, r *http.Request) {
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "agent_desktop_control_approve", chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	if !canEditRuntime(member, rt) {
		writeError(w, http.StatusForbidden, "only runtime owners and workspace admins can approve control")
		return
	}
	request, ok := h.mirrorControls.pending(
		uuidToString(rt.ID),
		chi.URLParam(r, "requestId"),
		time.Now(),
	)
	if !ok {
		writeError(w, http.StatusNotFound, "control request not found")
		return
	}
	status, err := h.startAgentDesktopControl(r, rt, request.UserID, time.Now(), request)
	if err != nil {
		writeMirrorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *Handler) DenyAgentDesktopControl(w http.ResponseWriter, r *http.Request) {
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "agent_desktop_control_deny", chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	if !canEditRuntime(member, rt) {
		writeError(w, http.StatusForbidden, "only runtime owners and workspace admins can deny control")
		return
	}
	if !h.mirrorControls.deny(uuidToString(rt.ID), chi.URLParam(r, "requestId")) {
		writeError(w, http.StatusNotFound, "control request not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) SendAgentDesktopControlInput(w http.ResponseWriter, r *http.Request) {
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "agent_desktop_control_input", chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	lease, ok := h.mirrorControls.touch(uuidToString(rt.ID), requestUserID(r), time.Now())
	if !ok {
		writeMirrorReason(w, http.StatusConflict, "control_not_active")
		return
	}
	var input protocol.AgentDesktopControlInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 80*1024)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid control input")
		return
	}
	if err := input.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid control input")
		return
	}
	result, err := h.DaemonHub.SendAgentDesktopControlInput(
		r.Context(),
		lease.WorkspaceID,
		lease.RuntimeID,
		lease.DaemonID,
		lease.ID,
		input,
	)
	if err != nil || result.State != "ok" {
		writeMirrorReason(w, http.StatusServiceUnavailable, "control_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clipboard": result.Clipboard})
}

func (h *Handler) StopAgentDesktopControl(w http.ResponseWriter, r *http.Request) {
	rt, member, ok := h.requireRuntimeReadAccess(w, r, "agent_desktop_control_stop", chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	lease := h.mirrorControls.end(uuidToString(rt.ID), requestUserID(r))
	if lease == nil && canEditRuntime(member, rt) {
		lease = h.mirrorControls.end(uuidToString(rt.ID), "")
	}
	if lease == nil {
		h.mirrorControls.cancelRequest(uuidToString(rt.ID), requestUserID(r))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_, err := h.DaemonHub.StopAgentDesktopControl(
		r.Context(),
		lease.WorkspaceID,
		lease.RuntimeID,
		lease.DaemonID,
		lease.ID,
	)
	if err != nil && !errors.Is(err, context.Canceled) {
		h.mirrorControls.end(lease.RuntimeID, "")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) startAgentDesktopControl(
	r *http.Request,
	rt db.AgentRuntime,
	userID string,
	now time.Time,
	request *mirrorControlRequest,
) (mirrorControlStatusResponse, error) {
	if !rt.DaemonID.Valid || rt.Status != "online" {
		return mirrorControlStatusResponse{}, errMirrorControlDaemonUnavailable
	}
	controlID := "mc-" + uuid.NewString()
	result, err := h.DaemonHub.StartAgentDesktopControl(
		r.Context(),
		uuidToString(rt.WorkspaceID),
		uuidToString(rt.ID),
		rt.DaemonID.String,
		controlID,
		"multica mirror control "+strings.TrimSpace(userID),
	)
	if err != nil {
		return mirrorControlStatusResponse{}, err
	}
	if result.State != "started" {
		return mirrorControlStatusResponse{}, errors.New("agent-desktop control failed to start")
	}
	var lease *mirrorControlLease
	var activated bool
	if request != nil {
		lease, activated = h.mirrorControls.activate(request, rt.DaemonID.String, controlID, now)
	} else {
		lease, activated = h.mirrorControls.activateDirect(
			uuidToString(rt.WorkspaceID),
			uuidToString(rt.ID),
			rt.DaemonID.String,
			userID,
			controlID,
			now,
		)
	}
	if !activated {
		_, _ = h.DaemonHub.StopAgentDesktopControl(
			r.Context(),
			uuidToString(rt.WorkspaceID),
			uuidToString(rt.ID),
			rt.DaemonID.String,
			controlID,
		)
		return mirrorControlStatusResponse{}, errors.New("another controller is active")
	}
	return mirrorControlResponse(mirrorControlStatus{State: "controlling", Lease: lease}), nil
}

func mirrorControlResponse(status mirrorControlStatus) mirrorControlStatusResponse {
	response := mirrorControlStatusResponse{State: status.State}
	if status.Lease != nil {
		response.ControlID = status.Lease.ID
		response.ControllerID = status.Lease.UserID
		response.LeaseExpiresAt = status.Lease.LastActive.Add(mirrorControlIdleTimeout).UTC().Format(time.RFC3339)
		response.LeaseMaxExpiresAt = status.Lease.StartedAt.Add(mirrorControlMaxLease).UTC().Format(time.RFC3339)
	}
	if status.Request != nil {
		response.RequestID = status.Request.ID
		response.RequesterID = status.Request.UserID
	}
	return response
}
