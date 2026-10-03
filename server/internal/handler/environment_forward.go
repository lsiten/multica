package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ForwardRuntimeEnvironment authorizes the human runtime owner before relaying
// opaque identities. It never accepts or executes client-selected local paths.
func (h *Handler) ForwardRuntimeEnvironment(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "human runtime owner required")
		return
	}
	runtime, _, ok := h.requireRuntimeReadAccess(w, r, obsmetrics.RuntimeLookupSourceRuntimeAPI, chi.URLParam(r, "runtimeId"))
	if !ok {
		return
	}
	if uuidToString(runtime.WorkspaceID) != ctxWorkspaceID(r.Context()) {
		writeError(w, http.StatusNotFound, "runtime not found in selected workspace")
		return
	}
	if uuidToString(runtime.OwnerID) != user {
		writeError(w, http.StatusForbidden, "runtime owner required")
		return
	}
	var input protocol.EnvironmentCommand
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || input.Validate() != nil {
		writeError(w, http.StatusBadRequest, "invalid environment command")
		return
	}
	var extra json.RawMessage
	if decoder.Decode(&extra) != io.EOF {
		writeError(w, http.StatusBadRequest, "single environment command required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	exchange, err := h.localReviewRelay.enqueue(protocol.LocalReviewCommand{
		WorkspaceID: uuidToString(runtime.WorkspaceID), RuntimeID: uuidToString(runtime.ID), ActorID: user, Action: "environment", Environment: &input,
	})
	if err != nil {
		writeError(w, http.StatusTooManyRequests, "runtime relay busy")
		return
	}
	result, err := h.localReviewRelay.wait(ctx, exchange)
	if err != nil {
		writeError(w, http.StatusGatewayTimeout, "runtime did not respond; refresh operation status before retrying")
		return
	}
	if result.Error != "" {
		writeError(w, http.StatusConflict, result.Error)
		return
	}
	if !json.Valid(result.Page) {
		writeError(w, http.StatusBadGateway, "invalid runtime environment response")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result.Page)
}
