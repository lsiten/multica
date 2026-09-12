package daemonws

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// MirrorViewerHandler processes authenticated daemon mirror viewer transitions.
type MirrorViewerHandler func(ctx context.Context, identity ClientIdentity, payload protocol.MirrorViewerPayload) error

func (h *Hub) SetMirrorViewerHandler(fn MirrorViewerHandler) {
	if h == nil {
		return
	}
	h.mirrorViewerMu.Lock()
	h.onMirrorViewer = fn
	h.mirrorViewerMu.Unlock()
}

func (h *Hub) mirrorViewerHandler() MirrorViewerHandler {
	h.mirrorViewerMu.RLock()
	defer h.mirrorViewerMu.RUnlock()
	return h.onMirrorViewer
}

func (c *client) handleMirrorViewerFrame(raw json.RawMessage) {
	var payload protocol.MirrorViewerPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		slog.Debug("daemon websocket mirror viewer invalid payload", "error", err, "daemon_id", c.identity.DaemonID)
		return
	}
	if err := payload.Validate(); err != nil {
		slog.Debug("daemon websocket mirror viewer rejected", "error", err, "daemon_id", c.identity.DaemonID)
		return
	}
	if payload.DaemonID != c.identity.DaemonID || !c.allowsRuntime(payload.RuntimeID) || !c.identity.AllowsWorkspace(payload.WorkspaceID) {
		slog.Warn("daemon websocket mirror viewer for unauthorized scope", "daemon_id", c.identity.DaemonID, "runtime_id", payload.RuntimeID)
		return
	}
	handler := c.hub.mirrorViewerHandler()
	if handler == nil {
		return
	}
	go func() {
		if err := handler(c.ctx, c.identity, payload); err != nil {
			slog.Debug("daemon websocket mirror viewer handler failed", "error", err, "daemon_id", c.identity.DaemonID, "runtime_id", payload.RuntimeID)
		}
	}()
}
