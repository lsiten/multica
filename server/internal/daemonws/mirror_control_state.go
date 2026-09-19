package daemonws

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// MirrorControlStateHandler processes authenticated daemon control transitions.
// The payload identifies a controller and source, but never contains input.
type MirrorControlStateHandler func(ctx context.Context, identity ClientIdentity, payload protocol.MirrorControlStatePayload) error

func (h *Hub) SetMirrorControlStateHandler(fn MirrorControlStateHandler) {
	if h == nil {
		return
	}
	h.mirrorControlStateMu.Lock()
	h.onMirrorControlState = fn
	h.mirrorControlStateMu.Unlock()
}

func (h *Hub) mirrorControlStateHandler() MirrorControlStateHandler {
	h.mirrorControlStateMu.RLock()
	defer h.mirrorControlStateMu.RUnlock()
	return h.onMirrorControlState
}

func (c *client) handleMirrorControlStateFrame(raw json.RawMessage) {
	var payload protocol.MirrorControlStatePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		slog.Debug("daemon websocket mirror control state invalid payload", "error", err, "daemon_id", c.identity.DaemonID)
		return
	}
	if err := payload.Validate(); err != nil {
		slog.Debug("daemon websocket mirror control state rejected", "error", err, "daemon_id", c.identity.DaemonID)
		return
	}
	if payload.DaemonID != c.identity.DaemonID || !c.allowsRuntime(payload.RuntimeID) || !c.identity.AllowsWorkspace(payload.WorkspaceID) {
		slog.Warn("daemon websocket mirror control state for unauthorized scope", "daemon_id", c.identity.DaemonID, "runtime_id", payload.RuntimeID)
		return
	}
	handler := c.hub.mirrorControlStateHandler()
	if handler == nil {
		return
	}
	go func() {
		if err := handler(c.ctx, c.identity, payload); err != nil {
			slog.Debug("daemon websocket mirror control state handler failed", "error", err, "daemon_id", c.identity.DaemonID, "runtime_id", payload.RuntimeID)
		}
	}()
}
