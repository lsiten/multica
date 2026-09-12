package daemonws

import "context"

// DisconnectHandler observes an authenticated daemon connection after it is
// removed from the hub.
type DisconnectHandler func(ctx context.Context, identity ClientIdentity)

func (h *Hub) SetDisconnectHandler(handler DisconnectHandler) {
	if h == nil {
		return
	}
	h.disconnectMu.Lock()
	h.onDisconnect = handler
	h.disconnectMu.Unlock()
}

func (h *Hub) disconnectHandler() DisconnectHandler {
	h.disconnectMu.RLock()
	defer h.disconnectMu.RUnlock()
	return h.onDisconnect
}
