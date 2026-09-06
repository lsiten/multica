package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestNotificationBot sends only on an explicit user action, including when the
// destination is disabled. It never borrows an agent integration's credentials.
func (h *Handler) TestNotificationBot(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if h.NotificationBots == nil {
		writeError(w, http.StatusServiceUnavailable, "notification bots are not configured on the server")
		return
	}
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	bot, err := h.Queries.GetNotificationBot(r.Context(), db.GetNotificationBotParams{ID: id, WorkspaceID: parseUUID(ctxWorkspaceID(r.Context())), UserID: parseUUID(userID)})
	if err != nil {
		notificationBotError(w, r, err)
		return
	}
	config, err := h.NotificationBots.Open(bot.Platform, bot.Credentials)
	if err == nil {
		err = h.NotificationBots.Sender.Send(r.Context(), config, "Multica 通知机器人测试 / Notification bot test")
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
