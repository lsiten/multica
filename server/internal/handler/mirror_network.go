package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type mirrorNetworkServerResponse struct {
	URLs          []string `json:"urls"`
	Username      string   `json:"username,omitempty"`
	HasCredential bool     `json:"has_credential"`
}

type mirrorBuiltinNetworkResponse struct {
	Enabled              bool     `json:"enabled"`
	Available            bool     `json:"available"`
	Host                 string   `json:"host,omitempty"`
	Port                 int      `json:"port,omitempty"`
	Transports           []string `json:"transports,omitempty"`
	CredentialTTLSeconds int64    `json:"credential_ttl_seconds,omitempty"`
}

type mirrorNetworkResponse struct {
	Source         string                        `json:"source"`
	Locked         bool                          `json:"locked"`
	CanManage      bool                          `json:"can_manage"`
	TURNConfigured bool                          `json:"turn_configured"`
	Mode           string                        `json:"mode"`
	Builtin        mirrorBuiltinNetworkResponse  `json:"builtin"`
	Custom         []mirrorNetworkServerResponse `json:"custom"`
}

func (h *Handler) GetWorkspaceMirrorNetwork(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	_, member, settings, ok := h.loadMirrorNetworkSettings(w, r, workspaceID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.mirrorNetworkResponse(settings, roleAllowed(member.Role, "owner", "admin")))
}

func (h *Handler) UpdateWorkspaceMirrorNetwork(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	ws, _, current, ok := h.loadMirrorNetworkSettings(w, r, workspaceID)
	if !ok {
		return
	}
	if len(h.cfg.MirrorICE.ICEServers) > 0 {
		writeError(w, http.StatusConflict, "mirror network is locked by deployment configuration")
		return
	}
	var req updateMirrorNetworkRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	next, err := h.normalizeMirrorNetworkRequest(req, current)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	merged, err := mirror.MarshalNetworkSettings(ws.Settings, next)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode workspace settings")
		return
	}
	updated, err := h.Queries.UpdateWorkspace(r.Context(), db.UpdateWorkspaceParams{ID: ws.ID, Settings: merged})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update mirror network")
		return
	}
	updatedSettings, err := mirror.ParseNetworkSettings(updated.Settings)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stored mirror network settings are invalid")
		return
	}
	writeJSON(w, http.StatusOK, h.mirrorNetworkResponse(updatedSettings, true))
}

func (h *Handler) loadMirrorNetworkSettings(w http.ResponseWriter, r *http.Request, workspaceID string) (db.Workspace, db.Member, mirror.NetworkSettings, bool) {
	idUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.Workspace{}, db.Member{}, mirror.NetworkSettings{}, false
	}
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return db.Workspace{}, db.Member{}, mirror.NetworkSettings{}, false
	}
	ws, err := h.Queries.GetWorkspace(r.Context(), idUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return db.Workspace{}, db.Member{}, mirror.NetworkSettings{}, false
	}
	settings, err := mirror.ParseNetworkSettings(ws.Settings)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stored mirror network settings are invalid")
		return db.Workspace{}, db.Member{}, mirror.NetworkSettings{}, false
	}
	return ws, member, settings, true
}

func (h *Handler) mirrorNetworkResponse(settings mirror.NetworkSettings, canManage bool) mirrorNetworkResponse {
	custom := h.decryptMirrorNetworkServers(settings.Servers)
	source := h.mirrorNetworkSource(settings, custom)
	return mirrorNetworkResponse{
		Source:         source,
		Locked:         len(h.cfg.MirrorICE.ICEServers) > 0,
		CanManage:      canManage && len(h.cfg.MirrorICE.ICEServers) == 0,
		TURNConfigured: h.mirrorNetworkConfigured(source, custom),
		Mode:           settings.Mode,
		Builtin: mirrorBuiltinNetworkResponse{
			Enabled:              h.cfg.MirrorBuiltinTURN.Enabled,
			Available:            h.cfg.MirrorBuiltinTURN.Configured(),
			Host:                 strings.TrimSpace(h.cfg.MirrorBuiltinTURN.Host),
			Port:                 h.cfg.MirrorBuiltinTURN.Port,
			Transports:           mirrorNetworkTransports(h.cfg.MirrorBuiltinTURN.Transports),
			CredentialTTLSeconds: int64(h.cfg.MirrorBuiltinTURN.TTL / time.Second),
		},
		Custom: mirrorNetworkCustomResponse(custom),
	}
}

func mirrorNetworkCustomResponse(servers []mirror.ICEServer) []mirrorNetworkServerResponse {
	response := make([]mirrorNetworkServerResponse, 0, len(servers))
	for _, server := range servers {
		response = append(response, mirrorNetworkServerResponse{
			URLs:          append([]string(nil), server.URLs...),
			Username:      server.Username,
			HasCredential: server.Credential != "",
		})
	}
	return response
}

func (h *Handler) mirrorNetworkSource(settings mirror.NetworkSettings, custom []mirror.ICEServer) string {
	if len(h.cfg.MirrorICE.ICEServers) > 0 {
		return mirror.NetworkSourceEnv
	}
	if settings.Mode == mirror.NetworkModeCustom && len(custom) > 0 {
		return mirror.NetworkSourceCustom
	}
	if settings.Mode == mirror.NetworkModeDisabled {
		return mirror.NetworkSourceDisabled
	}
	if h.cfg.MirrorBuiltinTURN.Configured() {
		return mirror.NetworkSourceBuiltin
	}
	return mirror.NetworkSourceBuiltinUnavailable
}

func (h *Handler) mirrorNetworkConfigured(source string, custom []mirror.ICEServer) bool {
	switch source {
	case mirror.NetworkSourceEnv:
		return h.cfg.MirrorICE.TURNConfigured
	case mirror.NetworkSourceBuiltin:
		return true
	case mirror.NetworkSourceCustom:
		for _, server := range custom {
			if mirror.HasTURNURL(server.URLs) {
				return true
			}
		}
	}
	return false
}

func encodeSecret(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func decodeSecret(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
}

func mirrorNetworkTransports(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}
