package handler

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/auth"
)

var applicationTunnelUpgrader = websocket.Upgrader{ReadBufferSize: 32 << 10, WriteBufferSize: 32 << 10, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}

// ConnectApplicationControl binds a dedicated outbound channel to its authenticated runtime.
func (h *Handler) ConnectApplicationControl(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.applicationDaemonRuntime(w, r, r.URL.Query().Get("daemon_id"))
	if !ok {
		return
	}
	if !h.authorizeApplicationServiceSocket(w, r, runtime) {
		return
	}
	connection, err := applicationTunnelUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ctx, stop := h.applicationServiceSocket(r, connection)
	defer stop()
	if err = h.ApplicationGateway.Control(ctx, uuidToString(runtime.ID), connection); err != nil {
		return
	}
}

// ConnectApplicationStream consumes a one-use request credential after runtime authentication.
func (h *Handler) ConnectApplicationStream(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.applicationDaemonRuntime(w, r, r.URL.Query().Get("daemon_id"))
	if !ok {
		return
	}
	if !h.authorizeApplicationServiceSocket(w, r, runtime) {
		return
	}
	connection, err := applicationTunnelUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ctx, stop := h.applicationServiceSocket(r, connection)
	defer stop()
	connection.SetReadLimit(1024)
	connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	var input struct {
		StreamID string `json:"stream_id"`
		Token    string `json:"token"`
	}
	if err = connection.ReadJSON(&input); err != nil {
		connection.Close()
		return
	}
	connection.SetReadDeadline(time.Time{})
	if auth.ApplicationServiceIdentityFromContext(r.Context()).TokenHash != "" {
		err = h.ApplicationGateway.AttachStreamScoped(ctx, uuidToString(runtime.ID), uuidToString(runtime.WorkspaceID), input.StreamID, input.Token, connection)
	} else {
		err = h.ApplicationGateway.AttachStream(ctx, uuidToString(runtime.ID), uuidToString(runtime.WorkspaceID), input.StreamID, input.Token, connection)
	}
	if err != nil {
		connection.Close()
	}
}
