package handler

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) runtimeMirrorPlan(w http.ResponseWriter, r *http.Request, rt db.AgentRuntime) (mirror.ICEPlan, bool) {
	ws, err := h.Queries.GetWorkspace(r.Context(), rt.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load mirror network settings")
		return mirror.ICEPlan{}, false
	}
	return h.resolveMirrorPlan(ws.Settings, time.Now(), requestUserID(r)), true
}

func (h *Handler) resolveMirrorPlan(rawSettings []byte, now time.Time, identity string) mirror.ICEPlan {
	settings, err := mirror.ParseNetworkSettings(rawSettings)
	if err != nil {
		slog.Warn("mirror network settings unreadable", "error", err)
		return mirror.ICEPlan{ICEServers: []mirror.ICEServer{}}
	}
	custom := h.decryptMirrorNetworkServers(settings.Servers)
	plan, _ := mirror.ResolveNetworkPlan(
		settings,
		h.cfg.MirrorICE,
		h.cfg.MirrorBuiltinTURN,
		custom,
		now,
		identity,
	)
	return plan
}

func (h *Handler) decryptMirrorNetworkServers(stored []mirror.StoredICEServer) []mirror.ICEServer {
	servers := make([]mirror.ICEServer, 0, len(stored))
	for _, item := range stored {
		server := mirror.ICEServer{URLs: append([]string(nil), item.URLs...), Username: item.Username}
		if item.CredentialEncrypted == "" {
			servers = append(servers, server)
			continue
		}
		sealed, err := decodeSecret(item.CredentialEncrypted)
		if err != nil || h.cfg.MirrorNetworkSecretBox == nil {
			slog.Warn("mirror network credential unavailable", "error", err)
		} else if credential, err := h.cfg.MirrorNetworkSecretBox.Open(sealed); err == nil {
			server.Credential = string(credential)
		} else {
			slog.Warn("mirror network credential could not be decrypted", "error", err)
		}
		servers = append(servers, server)
	}
	return servers
}
