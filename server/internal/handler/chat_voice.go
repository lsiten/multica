package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TranscribeChatVoice forwards bounded audio without creating a chat, task, attachment, or database row.
func (h *Handler) TranscribeChatVoice(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "human actor required")
		return
	}
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	workspaceID := uuidToString(agent.WorkspaceID)
	userID := requestUserID(r)
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	if agent.ArchivedAt.Valid || !h.canInvokeAgent(r.Context(), agent, actorType, actorID, h.invokeOriginatorFromRequest(r, actorType, actorID), workspaceID) {
		writeError(w, http.StatusForbidden, "agent_unavailable")
		return
	}
	if !agent.RuntimeID.Valid {
		writeError(w, http.StatusConflict, "runtime_required")
		return
	}
	rt, _, ok := h.requireRuntimeReadAccess(w, r, "chat_voice", uuidToString(agent.RuntimeID))
	if !ok {
		return
	}
	if rt.WorkspaceID != agent.WorkspaceID {
		writeError(w, http.StatusForbidden, "agent_unavailable")
		return
	}
	if !runtimeHasCapability(rt.Metadata, protocol.DaemonCapabilityVoiceTranscriptionV1) {
		writeError(w, http.StatusNotImplemented, "voice_upgrade_required")
		return
	}
	if rt.Status != "online" || !rt.DaemonID.Valid || h.DaemonHub == nil {
		writeError(w, http.StatusServiceUnavailable, "daemon_unavailable")
		return
	}
	var audio protocol.VoiceAudio
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, protocol.MaxVoiceRequestBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&audio) != nil || decoder.Decode(new(json.RawMessage)) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_audio")
		return
	}
	if _, err := audio.Decode(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_audio")
		return
	}
	result, err := h.DaemonHub.TranscribeVoice(r.Context(), uuidToString(rt.WorkspaceID), uuidToString(rt.ID), rt.DaemonID.String, audio)
	if err != nil {
		switch {
		case errors.Is(err, daemonws.ErrVscreenCapacity):
			writeError(w, http.StatusTooManyRequests, "voice_busy")
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, "voice_timeout")
		case errors.Is(err, context.Canceled):
			return
		default:
			writeError(w, http.StatusServiceUnavailable, "daemon_unavailable")
		}
		return
	}
	if result.Reason != "" {
		writeError(w, http.StatusServiceUnavailable, result.Reason)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Text string `json:"text"`
	}{Text: result.Text})
}
