package daemonws

import "github.com/multica-ai/multica/server/pkg/protocol"

// SendManagedMirrorOffer pins source authorization and offer delivery to the queried connection.
func (h *Hub) SendManagedMirrorOffer(runtimeID, generation string, payload protocol.MirrorOfferPayload) bool {
	if payload.Validate() != nil {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	c := h.vscreenClientLocked(payload.WorkspaceID, runtimeID, payload.DaemonID)
	return c != nil && c.vscreenGeneration == generation && c.trySend(mustMarshalRaw(protocol.Message{Type: protocol.EventMirrorOffer, Payload: mustMarshalRaw(payload)}))
}

// SendViewerRenew targets the same connection that received the initial grant.
func (h *Hub) SendViewerRenew(daemonID string, payload protocol.MirrorViewerRenewPayload) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c := h.vscreenClientLocked(payload.Grant.WorkspaceID, payload.Grant.RuntimeID, daemonID)
	return c != nil && c.vscreenGeneration == payload.DaemonGeneration && c.trySend(mustMarshalRaw(protocol.Message{Type: protocol.EventMirrorViewerRenew, Payload: mustMarshalRaw(payload)}))
}

// SendViewerRevoke cannot revoke unrelated runtime or viewer leases.
func (h *Hub) SendViewerRevoke(daemonID string, payload protocol.MirrorViewerRevokePayload) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c := h.vscreenClientLocked(payload.WorkspaceID, payload.RuntimeID, daemonID)
	return c != nil && c.vscreenGeneration == payload.DaemonGeneration && c.trySend(mustMarshalRaw(protocol.Message{Type: protocol.EventMirrorViewerRevoke, Payload: mustMarshalRaw(payload)}))
}
