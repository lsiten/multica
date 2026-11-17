package main

import (
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/mobilepush"
)

func configureMobilePush(h *handler.Handler) {
	teamID := strings.TrimSpace(os.Getenv("MULTICA_APNS_TEAM_ID"))
	topics := strings.Split(os.Getenv("MULTICA_APNS_TOPICS"), ",")
	senders := make(map[string]mobilepush.Sender)
	for _, environment := range []string{"production", "sandbox"} {
		prefix := "MULTICA_APNS_" + strings.ToUpper(environment)
		keyID := strings.TrimSpace(os.Getenv(prefix + "_KEY_ID"))
		file := strings.TrimSpace(os.Getenv(prefix + "_PRIVATE_KEY_FILE"))
		if keyID == "" && file == "" {
			continue
		}
		key, err := os.ReadFile(file)
		if err != nil {
			slog.Warn("APNs key could not be loaded; check the configured private key file", "environment", environment)
			continue
		}
		client := &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true,
				DialContext:  (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				MaxIdleConns: 10, MaxIdleConnsPerHost: 5, IdleConnTimeout: time.Hour,
				TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		}
		sender, err := mobilepush.NewAPNsSender(mobilepush.Credentials{TeamID: teamID, KeyID: keyID, PrivateKey: key}, client)
		if err != nil {
			slog.Warn("APNs credentials are invalid; check team ID, key ID and the P8 key", "environment", environment)
			continue
		}
		senders[environment] = sender
	}
	worker := mobilepush.NewWorker(h.Queries, senders, topics)
	if worker.Configuration().Enabled {
		h.MobilePush = worker
	}
}

func registerMobilePushRoutes(router chi.Router, h *handler.Handler) {
	router.Get("/api/mobile-push/config", h.MobilePushConfiguration)
	router.With(handler.RequireHumanActor).Put("/api/mobile-push/devices", h.RegisterMobilePushDevice)
	router.With(handler.RequireHumanActor).Delete("/api/mobile-push/devices", h.DeleteMobilePushDevice)
}
