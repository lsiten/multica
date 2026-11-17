package handler

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type mobilePushDeviceRequest struct {
	Token       string `json:"token"`
	Topic       string `json:"topic"`
	Environment string `json:"environment"`
}

func (h *Handler) MobilePushConfiguration(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.MobilePush.Configuration())
}

func decodeMobilePushDevice(w http.ResponseWriter, r *http.Request) (mobilePushDeviceRequest, bool) {
	var request mobilePushDeviceRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid push device")
		return request, false
	}
	request.Token = strings.ToLower(strings.TrimSpace(request.Token))
	request.Topic = strings.TrimSpace(request.Topic)
	if request.Token == "" || len(request.Token) > 4096 || request.Topic == "" || len(request.Topic) > 255 || (request.Environment != "production" && request.Environment != "sandbox") {
		writeError(w, http.StatusBadRequest, "invalid push device")
		return request, false
	}
	if _, err := hex.DecodeString(request.Token); err != nil {
		writeError(w, http.StatusBadRequest, "invalid push device token")
		return request, false
	}
	return request, true
}

func (h *Handler) RegisterMobilePushDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	request, ok := decodeMobilePushDevice(w, r)
	if !ok {
		return
	}
	if !h.MobilePush.Allows(request.Topic, request.Environment) {
		writeErrorCode(w, http.StatusServiceUnavailable, "mobile_push_unavailable", "push notifications are not configured for this app and environment")
		return
	}
	if _, err := h.Queries.RegisterMobilePushDevice(r.Context(), db.RegisterMobilePushDeviceParams{
		UserID: parseUUID(userID), Token: request.Token, Topic: request.Topic, Environment: request.Environment,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not register push device")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"registered": true})
}

func (h *Handler) DeleteMobilePushDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	request, ok := decodeMobilePushDevice(w, r)
	if !ok {
		return
	}
	if err := h.Queries.DeleteMobilePushDevice(r.Context(), db.DeleteMobilePushDeviceParams{
		UserID: parseUUID(userID), Token: request.Token, Topic: request.Topic, Environment: request.Environment,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not remove push device")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
