package handler

import (
	"log/slog"
	"net/http"
	"strings"
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
		h.builtinNetwork(settings),
		custom,
		now,
		identity,
	)
	return plan
}

// builtinNetwork assembles the deployment/workspace relay chain. Precedence:
//  1. workspace-managed Cloudflare TURN key (Settings -> Screen mirroring)
//  2. deployment-level Cloudflare TURN key from the environment
//  3. bundled coturn service
func (h *Handler) builtinNetwork(settings mirror.NetworkSettings) mirror.BuiltinNetwork {
	providers := make([]mirror.BuiltinNetwork, 0, 3)
	if provider := h.workspaceCloudflareProvider(settings.Cloudflare); provider != nil {
		providers = append(providers, provider)
	}
	if h.cfg.MirrorCloudflareTURN != nil && h.cfg.MirrorCloudflareTURN.Configured() {
		providers = append(providers, h.cfg.MirrorCloudflareTURN)
	}
	if h.cfg.MirrorBuiltinTURN.Configured() {
		providers = append(providers, h.cfg.MirrorBuiltinTURN)
	}
	return mirror.NewBuiltinChain(providers...)
}

func (h *Handler) workspaceCloudflareProvider(stored *mirror.StoredCloudflareTURN) *mirror.CloudflareTURNProvider {
	if stored == nil || strings.TrimSpace(stored.KeyID) == "" || strings.TrimSpace(stored.APITokenEncrypted) == "" {
		return nil
	}
	sealed, err := decodeSecret(stored.APITokenEncrypted)
	if err != nil || h.cfg.MirrorNetworkSecretBox == nil {
		slog.Warn("workspace Cloudflare TURN token unavailable", "error", err)
		return nil
	}
	token, err := h.cfg.MirrorNetworkSecretBox.Open(sealed)
	if err != nil {
		slog.Warn("workspace Cloudflare TURN token could not be decrypted", "error", err)
		return nil
	}
	return h.cloudflareTURNProviders.provider(stored.KeyID, string(token))
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
