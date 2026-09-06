package main

import (
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/notificationbot"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

func configureNotificationBots(h *handler.Handler) {
	key, err := secretbox.LoadKey("MULTICA_NOTIFICATION_SECRET_KEY")
	if err != nil {
		slog.Info("notification bots disabled; configure MULTICA_NOTIFICATION_SECRET_KEY to enable")
		return
	}
	box, err := secretbox.New(key)
	if err != nil {
		slog.Error("notification bot encryption initialization failed", "error", err)
		return
	}
	appURL := ""
	for _, name := range []string{"MULTICA_APP_URL", "FRONTEND_ORIGIN"} {
		value := strings.TrimSpace(os.Getenv(name))
		parsed, parseErr := url.Parse(value)
		if parseErr == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" {
			appURL = value
			break
		}
	}
	h.NotificationBots = notificationbot.NewWorker(h.Queries, box, appURL)
}
