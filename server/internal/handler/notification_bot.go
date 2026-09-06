package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/notificationbot"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type notificationBotResponse struct {
	ID             pgtype.UUID        `json:"id"`
	Name           string             `json:"name"`
	Platform       string             `json:"platform"`
	IsEnabled      bool               `json:"is_enabled"`
	LastDeliveryAt pgtype.Timestamptz `json:"last_delivery_at"`
	LastError      string             `json:"last_error"`
}

func notificationBotPublic(bot db.NotificationBot) notificationBotResponse {
	return notificationBotResponse{ID: bot.ID, Name: bot.Name, Platform: bot.Platform, IsEnabled: bot.IsEnabled, LastDeliveryAt: bot.LastDeliveryAt, LastError: bot.LastError}
}

func (h *Handler) ListNotificationBots(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	bots, err := h.Queries.ListNotificationBots(r.Context(), db.ListNotificationBotsParams{WorkspaceID: parseUUID(ctxWorkspaceID(r.Context())), UserID: parseUUID(userID)})
	if err != nil {
		notificationBotError(w, r, err)
		return
	}
	result := make([]notificationBotResponse, 0, len(bots))
	for _, bot := range bots {
		result = append(result, notificationBotPublic(bot))
	}
	writeJSON(w, http.StatusOK, struct {
		Available bool                      `json:"available"`
		Bots      []notificationBotResponse `json:"bots"`
	}{Available: h.NotificationBots != nil, Bots: result})
}

type saveNotificationBotRequest struct {
	Name        string                       `json:"name"`
	Platform    string                       `json:"platform"`
	IsEnabled   bool                         `json:"is_enabled"`
	Credentials *notificationbot.Credentials `json:"credentials"`
}

func (h *Handler) SaveNotificationBot(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if h.NotificationBots == nil {
		writeError(w, http.StatusServiceUnavailable, "notification bots require MULTICA_NOTIFICATION_SECRET_KEY on the server")
		return
	}
	var input saveNotificationBotRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || strings.TrimSpace(input.Name) == "" || len(input.Name) > 100 {
		writeError(w, http.StatusBadRequest, "invalid notification bot configuration")
		return
	}
	workspaceID := parseUUID(ctxWorkspaceID(r.Context()))
	ownerID := parseUUID(userID)
	id := chi.URLParam(r, "id")
	var botID pgtype.UUID
	if id != "" {
		botID, ok = parseUUIDOrBadRequest(w, id, "id")
		if !ok {
			return
		}
		existing, err := h.Queries.GetNotificationBot(r.Context(), db.GetNotificationBotParams{ID: botID, WorkspaceID: workspaceID, UserID: ownerID})
		if err != nil {
			notificationBotError(w, r, err)
			return
		}
		if existing.Platform != input.Platform {
			writeError(w, http.StatusBadRequest, "platform cannot be changed; create a new bot")
			return
		}
	} else if input.Credentials == nil {
		writeError(w, http.StatusBadRequest, "credentials are required")
		return
	}
	var ciphertext []byte
	if input.Credentials != nil {
		if _, err := notificationbot.ParseConfig(input.Platform, *input.Credentials); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		raw, err := json.Marshal(input.Credentials)
		if err != nil {
			notificationBotError(w, r, err)
			return
		}
		ciphertext, err = h.NotificationBots.Box.Seal(raw)
		if err != nil {
			notificationBotError(w, r, err)
			return
		}
	}
	var saved db.NotificationBot
	var err error
	if id == "" {
		bots, listErr := h.Queries.ListNotificationBots(r.Context(), db.ListNotificationBotsParams{WorkspaceID: workspaceID, UserID: ownerID})
		if listErr != nil {
			notificationBotError(w, r, listErr)
			return
		}
		if len(bots) >= 20 {
			writeError(w, http.StatusBadRequest, "at most 20 notification bots per workspace")
			return
		}
		saved, err = h.Queries.CreateNotificationBot(r.Context(), db.CreateNotificationBotParams{WorkspaceID: workspaceID, UserID: ownerID, Name: strings.TrimSpace(input.Name), Platform: input.Platform, Credentials: ciphertext, IsEnabled: input.IsEnabled})
	} else {
		saved, err = h.Queries.UpdateNotificationBot(r.Context(), db.UpdateNotificationBotParams{ID: botID, WorkspaceID: workspaceID, UserID: ownerID, Name: strings.TrimSpace(input.Name), Credentials: ciphertext, IsEnabled: input.IsEnabled})
	}
	if err != nil {
		notificationBotError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, notificationBotPublic(saved))
}

func (h *Handler) DeleteNotificationBot(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	count, err := h.Queries.DeleteNotificationBot(r.Context(), db.DeleteNotificationBotParams{ID: id, WorkspaceID: parseUUID(ctxWorkspaceID(r.Context())), UserID: parseUUID(userID)})
	if err != nil {
		notificationBotError(w, r, err)
		return
	}
	if count == 0 {
		writeError(w, http.StatusNotFound, "notification bot not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func notificationBotError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "notification bot not found")
		return
	}
	slog.Error("notification bot operation failed", append(logger.RequestAttrs(r), "error", err)...)
	writeError(w, http.StatusInternalServerError, "notification bot operation failed")
}
