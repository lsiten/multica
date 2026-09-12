package handler

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/multica-ai/multica/server/internal/mirror"
)

type mirrorNetworkServerRequest struct {
	URLs       json.RawMessage `json:"urls"`
	Username   *string         `json:"username"`
	Credential *string         `json:"credential"`
}

type updateMirrorNetworkRequest struct {
	Mode    string                       `json:"mode"`
	Servers []mirrorNetworkServerRequest `json:"servers"`
}

func (h *Handler) normalizeMirrorNetworkRequest(req updateMirrorNetworkRequest, current mirror.NetworkSettings) (mirror.NetworkSettings, error) {
	mode := strings.TrimSpace(req.Mode)
	if !mirror.KnownNetworkMode(mode) {
		return mirror.NetworkSettings{}, errors.New("mode must be builtin, custom, or disabled")
	}
	next := mirror.NetworkSettings{Mode: mode, Servers: []mirror.StoredICEServer{}}
	if mode != mirror.NetworkModeCustom {
		return next, nil
	}
	if len(req.Servers) == 0 {
		return mirror.NetworkSettings{}, errors.New("custom mode requires at least one TURN server")
	}
	if len(req.Servers) > 8 {
		return mirror.NetworkSettings{}, errors.New("at most 8 ICE servers are supported")
	}
	currentByKey := make(map[string]mirror.StoredICEServer, len(current.Servers))
	for _, server := range current.Servers {
		currentByKey[storedServerKey(server)] = server
	}
	for _, serverReq := range req.Servers {
		urls, err := parseMirrorICEURLs(serverReq.URLs)
		if err != nil {
			return mirror.NetworkSettings{}, err
		}
		normalized, err := normalizeMirrorNetworkURLs(urls)
		if err != nil {
			return mirror.NetworkSettings{}, err
		}
		username := ""
		if serverReq.Username != nil {
			username = strings.TrimSpace(*serverReq.Username)
		}
		stored := mirror.StoredICEServer{URLs: normalized, Username: username}
		if serverReq.Credential != nil {
			if *serverReq.Credential == "" {
				// An explicit empty value clears the credential; an omitted field preserves it.
			} else {
				if h.cfg.MirrorNetworkSecretBox == nil {
					return mirror.NetworkSettings{}, errors.New("custom TURN credentials require the server encryption key")
				}
				sealed, err := h.cfg.MirrorNetworkSecretBox.Seal([]byte(*serverReq.Credential))
				if err != nil {
					return mirror.NetworkSettings{}, errors.New("failed to encrypt TURN credential")
				}
				stored.CredentialEncrypted = encodeSecret(sealed)
			}
		} else if previous, ok := currentByKey[storedServerKey(stored)]; ok {
			stored.CredentialEncrypted = previous.CredentialEncrypted
		}
		next.Servers = append(next.Servers, stored)
	}
	if !hasMirrorTURNURL(next.Servers) {
		return mirror.NetworkSettings{}, errors.New("custom mode requires at least one turn: or turns: URL")
	}
	return next, nil
}

func hasMirrorTURNURL(servers []mirror.StoredICEServer) bool {
	for _, server := range servers {
		if mirror.HasTURNURL(server.URLs) {
			return true
		}
	}
	return false
}

func normalizeMirrorNetworkURLs(values []string) ([]string, error) {
	normalized := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, raw := range values {
		value, err := mirror.NormalizeICEURL(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func storedServerKey(server mirror.StoredICEServer) string {
	return strings.Join(server.URLs, "\n") + "\x00" + server.Username
}
