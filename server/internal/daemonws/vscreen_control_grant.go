package daemonws

import (
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// SendControlGrant delivers or renews one viewer's human input capability to
// exactly the authenticated daemon connection currently serving its runtime.
func (h *Hub) SendControlGrant(daemonID string, payload protocol.MirrorControlGrantPayload) bool {
	if payload.Grant.Validate(time.Now()) != nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	c := h.vscreenClientLocked(payload.WorkspaceID, payload.RuntimeID, daemonID)
	return c != nil && c.vscreenGeneration == payload.DaemonGeneration &&
		c.trySend(mustMarshalRaw(protocol.Message{Type: protocol.EventMirrorControlGrant, Payload: mustMarshalRaw(payload)}))
}

// SendControlRevoke immediately removes one viewer's input capability.
func (h *Hub) SendControlRevoke(daemonID string, payload protocol.MirrorControlRevokePayload) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c := h.vscreenClientLocked(payload.WorkspaceID, payload.RuntimeID, daemonID)
	return c != nil && c.vscreenGeneration == payload.DaemonGeneration &&
		c.trySend(mustMarshalRaw(protocol.Message{Type: protocol.EventMirrorControlRevoke, Payload: mustMarshalRaw(payload)}))
}
