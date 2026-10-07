package handler

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var applicationTunnelUpgrader = websocket.Upgrader{ReadBufferSize: 32 << 10, WriteBufferSize: 32 << 10, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}

// ConnectApplicationControl binds a dedicated outbound channel to its authenticated runtime.
func (h *Handler) ConnectApplicationControl(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.applicationDaemonRuntime(w, r, r.URL.Query().Get("daemon_id"))
	if !ok {
		return
	}
	connection, err := applicationTunnelUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	if err = h.ApplicationGateway.Control(r.Context(), uuidToString(runtime.ID), connection); err != nil {
		return
	}
}

// ConnectApplicationStream consumes a one-use request credential after runtime authentication.
func (h *Handler) ConnectApplicationStream(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.applicationDaemonRuntime(w, r, r.URL.Query().Get("daemon_id"))
	if !ok {
		return
	}
	connection, err := applicationTunnelUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
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
	if err = h.ApplicationGateway.AttachStream(r.Context(), uuidToString(runtime.ID), uuidToString(runtime.WorkspaceID), input.StreamID, input.Token, connection); err != nil {
		connection.Close()
	}
}
