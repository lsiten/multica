package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (relay *localReviewRelay) active(claim protocol.LocalReviewCommand) bool {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	exchange := relay.pending[claim.ID]
	return exchange != nil && exchange.claimed && !exchange.completed &&
		exchange.command.WorkspaceID == claim.WorkspaceID &&
		exchange.command.RuntimeID == claim.RuntimeID &&
		exchange.command.ClaimToken == claim.ClaimToken
}

// LocalReviewRelayStatus exposes only whether this authenticated claim still has
// a waiting caller. There is no cancellation queue or retained review payload.
func (h *Handler) LocalReviewRelayStatus(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.localReviewRuntime(w, r)
	if !ok {
		return
	}
	var input protocol.LocalReviewStatusRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input) != nil || input.ClaimToken == "" || len(input.ClaimToken) > 128 {
		writeError(w, http.StatusBadRequest, "invalid review claim")
		return
	}
	active := h.localReviewRelay.active(protocol.LocalReviewCommand{
		ID: chi.URLParam(r, "commandId"), ClaimToken: input.ClaimToken,
		WorkspaceID: uuidToString(runtime.WorkspaceID), RuntimeID: uuidToString(runtime.ID),
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, protocol.LocalReviewStatus{Active: &active})
}
