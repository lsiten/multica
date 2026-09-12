package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/mirror"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type createMirrorSessionRequest struct {
	ViewerID string                            `json:"viewer_id"`
	Offer    protocol.MirrorSessionDescription `json:"offer"`
}

type mirrorSessionResponse struct {
	mirror.SessionMetadata
	Answer    *protocol.MirrorSessionDescription `json:"answer,omitempty"`
	ICEConfig protocol.MirrorICEConfig           `json:"ice_config"`
}

func (h *Handler) GetMirrorICEConfig(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "mirror_ice_config", runtimeID)
	if !ok {
		return
	}
	plan, ok := h.runtimeMirrorPlan(w, r, rt)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, plan.Protocol())
}

func (h *Handler) CreateMirrorSession(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "mirror_session_create", runtimeID)
	if !ok {
		return
	}
	if !rt.DaemonID.Valid || strings.TrimSpace(rt.DaemonID.String) == "" {
		writeError(w, http.StatusConflict, "runtime is not connected to a daemon")
		return
	}
	if rt.Status != "online" {
		writeError(w, http.StatusConflict, "runtime is offline")
		return
	}
	if !runtimeHasCapability(rt.Metadata, protocol.DaemonCapabilityScreenMirrorV1) {
		writeError(w, http.StatusNotImplemented, "runtime daemon does not support screen mirroring")
		return
	}
	var req createMirrorSessionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, protocol.MaxMirrorSDPBytes+64*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	identity := mirror.SessionIdentity{
		WorkspaceID: uuidToString(rt.WorkspaceID),
		RuntimeID:   runtimeID,
		UserID:      requestUserID(r),
		DaemonID:    rt.DaemonID.String,
		ViewerID:    strings.TrimSpace(req.ViewerID),
	}
	icePlan, ok := h.runtimeMirrorPlan(w, r, rt)
	if !ok {
		return
	}
	session, err := h.MirrorSessions.Create(r.Context(), mirror.CreateSessionInput{Identity: identity, Offer: req.Offer})
	if err != nil {
		if errors.Is(err, protocol.ErrMirrorSDPTooLarge) || errors.Is(err, protocol.ErrInvalidMirrorDescription) || errors.Is(err, mirror.ErrInvalidSessionInput) {
			writeError(w, http.StatusBadRequest, "invalid mirror offer")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create mirror session")
		return
	}
	offer, err := h.MirrorSessions.ConsumeOffer(r.Context(), session.ID, identity)
	if err != nil {
		_ = h.MirrorSessions.Close(r.Context(), session.ID, identity)
		h.writeMirrorSessionError(w, err)
		return
	}
	payload := protocol.MirrorOfferPayload{
		SessionID: session.ID, WorkspaceID: session.WorkspaceID, RuntimeID: session.RuntimeID,
		UserID: session.UserID, DaemonID: session.DaemonID, ViewerID: session.ViewerID,
		Offer: offer, ICEConfig: icePlan.Protocol(), ExpiresAt: session.ExpiresAt,
	}
	if !h.DaemonHub.SendMirrorOffer(runtimeID, payload) {
		_ = h.MirrorSessions.Close(r.Context(), session.ID, identity)
		writeError(w, http.StatusServiceUnavailable, "runtime daemon is unavailable")
		return
	}
	writeJSON(w, http.StatusCreated, mirrorSessionResponse{
		SessionMetadata: session.Metadata(),
		ICEConfig:       icePlan.Protocol(),
	})
}

func (h *Handler) GetMirrorSession(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "mirror_session_status", runtimeID)
	if !ok {
		return
	}
	metadata, err := h.MirrorSessions.MetadataByID(r.Context(), chi.URLParam(r, "sessionId"))
	if err != nil {
		h.writeMirrorSessionError(w, err)
		return
	}
	if metadata.RuntimeID != runtimeID || metadata.UserID != requestUserID(r) {
		writeError(w, http.StatusNotFound, "mirror session not found")
		return
	}
	icePlan, ok := h.runtimeMirrorPlan(w, r, rt)
	if !ok {
		return
	}
	response := mirrorSessionResponse{SessionMetadata: metadata, ICEConfig: icePlan.Protocol()}
	if metadata.State == mirror.SessionStateAnswered {
		answer, answerErr := h.MirrorSessions.Answer(r.Context(), metadata.ID, mirror.SessionIdentity{
			WorkspaceID: metadata.WorkspaceID, RuntimeID: metadata.RuntimeID, UserID: metadata.UserID,
			DaemonID: metadata.DaemonID, ViewerID: metadata.ViewerID,
		})
		if answerErr != nil {
			h.writeMirrorSessionError(w, answerErr)
			return
		}
		response.Answer = &answer
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) CloseMirrorSession(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "mirror_session_close", runtimeID)
	if !ok {
		return
	}
	identity := h.mirrorIdentityFromRequest(r, rt)
	if err := h.MirrorSessions.Close(r.Context(), chi.URLParam(r, "sessionId"), identity); err != nil {
		h.writeMirrorSessionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HandleDaemonMirrorAnswer(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.MirrorAnswerPayload) error {
	if identity.DaemonID == "" || payload.DaemonID != identity.DaemonID || !identity.AllowsWorkspace(payload.WorkspaceID) {
		return mirror.ErrSessionIdentityMismatch
	}
	if len(identity.RuntimeIDs) > 0 && !mirrorContainsString(identity.RuntimeIDs, payload.RuntimeID) {
		return mirror.ErrSessionIdentityMismatch
	}
	return h.MirrorSessions.SetAnswer(ctx, payload.SessionID, mirror.SessionIdentity{
		WorkspaceID: payload.WorkspaceID, RuntimeID: payload.RuntimeID, UserID: payload.UserID,
		DaemonID: payload.DaemonID, ViewerID: payload.ViewerID,
	}, payload.Answer)
}

func (h *Handler) HandleDaemonMirrorAnswerFailure(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.MirrorAnswerFailurePayload) error {
	if identity.DaemonID == "" || payload.DaemonID != identity.DaemonID || !identity.AllowsWorkspace(payload.WorkspaceID) {
		return mirror.ErrSessionIdentityMismatch
	}
	if len(identity.RuntimeIDs) > 0 && !mirrorContainsString(identity.RuntimeIDs, payload.RuntimeID) {
		return mirror.ErrSessionIdentityMismatch
	}
	return h.MirrorSessions.SetFailure(ctx, payload.SessionID, mirror.SessionIdentity{
		WorkspaceID: payload.WorkspaceID,
		RuntimeID:   payload.RuntimeID,
		UserID:      payload.UserID,
		DaemonID:    payload.DaemonID,
		ViewerID:    payload.ViewerID,
	}, payload.Reason)
}

func (h *Handler) writeMirrorSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mirror.ErrSessionExpired), errors.Is(err, mirror.ErrSessionClosed), errors.Is(err, mirror.ErrSessionReplay):
		writeError(w, http.StatusConflict, "mirror session is no longer available")
	case errors.Is(err, mirror.ErrSessionIdentityMismatch), errors.Is(err, mirror.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, "mirror session not found")
	default:
		writeError(w, http.StatusInternalServerError, "failed to access mirror session")
	}
}

func (h *Handler) mirrorIdentityFromRequest(r *http.Request, rt db.AgentRuntime) mirror.SessionIdentity {
	return mirror.SessionIdentity{WorkspaceID: uuidToString(rt.WorkspaceID), RuntimeID: chi.URLParam(r, "runtimeId"), UserID: requestUserID(r), DaemonID: rt.DaemonID.String, ViewerID: r.URL.Query().Get("viewer_id")}
}

func mirrorContainsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
