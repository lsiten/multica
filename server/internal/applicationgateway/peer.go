package applicationgateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// PeerPath is served before platform authentication and accepts only cluster-scoped signed requests.
const PeerPath = "/internal/application-gateway"

type peerRequest struct {
	Mode        string                            `json:"mode"`
	RuntimeID   string                            `json:"runtime_id"`
	WorkspaceID string                            `json:"workspace_id"`
	StreamID    string                            `json:"stream_id,omitempty"`
	Token       string                            `json:"token,omitempty"`
	Request     protocol.ApplicationTunnelRequest `json:"request"`
}

type peerClaims struct {
	Digest string `json:"digest"`
	jwt.RegisteredClaims
}

func (c *cluster) connectPeer(ctx context.Context, origin string, request peerRequest) (*Stream, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	claims := peerClaims{Digest: hex.EncodeToString(digest[:]), RegisteredClaims: jwt.RegisteredClaims{Audience: jwt.ClaimStrings{"multica-application-peer-v1"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(30 * time.Second))}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(peerSigningKey(c.secret))
	if err != nil {
		return nil, err
	}
	address := "ws" + strings.TrimPrefix(origin, "http") + PeerPath
	connection, response, err := (&websocket.Dialer{HandshakeTimeout: 5 * time.Second}).DialContext(ctx, address, http.Header{"Authorization": {"Bearer " + token}})
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return nil, ErrOffline
	}
	if err := connection.WriteMessage(websocket.TextMessage, raw); err != nil {
		connection.Close()
		return nil, err
	}
	connection.SetReadDeadline(time.Now().Add(15 * time.Second))
	var reply struct {
		Ready bool `json:"ready"`
	}
	if err := connection.ReadJSON(&reply); err != nil || !reply.Ready {
		connection.Close()
		return nil, ErrOffline
	}
	connection.SetReadDeadline(time.Time{})
	return NewStream(connection, nil), nil
}

// ServePeer carries bounded binary data after checking a request-bound, expiring peer signature.
func (h *Hub) ServePeer(w http.ResponseWriter, r *http.Request) {
	if h.cluster == nil || r.Method != http.MethodGet || r.Header.Get("Origin") != "" {
		http.NotFound(w, r)
		return
	}
	var claims peerClaims
	parsed, err := jwt.ParseWithClaims(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), &claims, func(*jwt.Token) (any, error) { return peerSigningKey(h.cluster.secret), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience("multica-application-peer-v1"), jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid {
		http.Error(w, "invalid application peer credential", http.StatusUnauthorized)
		return
	}
	connection, err := (&websocket.Upgrader{ReadBufferSize: 32 << 10, WriteBufferSize: 32 << 10, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	connection.SetReadLimit(64 << 10)
	connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	kind, raw, err := connection.ReadMessage()
	if err != nil || kind != websocket.TextMessage {
		connection.Close()
		return
	}
	digest := sha256.Sum256(raw)
	if claims.Digest != hex.EncodeToString(digest[:]) {
		connection.Close()
		return
	}
	var request peerRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		connection.Close()
		return
	}
	connection.SetReadDeadline(time.Time{})
	switch request.Mode {
	case "open":
		if request.RuntimeID != request.Request.RuntimeID || request.WorkspaceID != request.Request.WorkspaceID {
			connection.Close()
			return
		}
		local, err := h.openLocal(r.Context(), request.Request)
		if err != nil {
			connection.Close()
			return
		}
		defer local.Close()
		if err := connection.WriteJSON(map[string]bool{"ready": true}); err != nil {
			connection.Close()
			return
		}
		if err := Bridge(NewStream(connection, nil), local); err != nil {
			return
		}
	case "attach":
		if err := connection.WriteJSON(map[string]bool{"ready": true}); err != nil {
			connection.Close()
			return
		}
		if _, err := h.Attach(request.RuntimeID, request.WorkspaceID, request.StreamID, request.Token, connection); err != nil {
			connection.Close()
		}
	default:
		connection.Close()
	}
}

// AttachStream delivers an authenticated daemon stream to its owner even when ingress hits another replica.
func (h *Hub) AttachStream(ctx context.Context, runtimeID, workspaceID, streamID, token string, connection *websocket.Conn) error {
	h.mu.Lock()
	local := h.streams[streamID] != nil
	h.mu.Unlock()
	if local || h.cluster == nil {
		_, err := h.Attach(runtimeID, workspaceID, streamID, token, connection)
		return err
	}
	encoded, err := h.cluster.redis.Get(ctx, streamOwnerKey(streamID)).Result()
	if err != nil {
		return ErrOffline
	}
	var current owner
	if err := json.Unmarshal([]byte(encoded), &current); err != nil {
		return ErrOffline
	}
	if current.Origin == h.cluster.origin {
		return ErrOffline
	}
	peer, err := h.cluster.connectPeer(ctx, current.Origin, peerRequest{Mode: "attach", RuntimeID: runtimeID, WorkspaceID: workspaceID, StreamID: streamID, Token: token})
	if err != nil {
		return err
	}
	defer peer.Close()
	return Bridge(NewStream(connection, nil), peer)
}

func (h *Hub) remoteOpen(ctx context.Context, request protocol.ApplicationTunnelRequest) (net.Conn, error) {
	current, err := h.cluster.runtimeOwner(ctx, request.RuntimeID)
	if err != nil {
		return nil, err
	}
	if current.Origin == h.cluster.origin {
		return h.openLocal(ctx, request)
	}
	return h.cluster.connectPeer(ctx, current.Origin, peerRequest{Mode: "open", RuntimeID: request.RuntimeID, WorkspaceID: request.WorkspaceID, Request: request})
}

func peerSigningKey(secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("multica-application-peer-v1"))
	return mac.Sum(nil)
}
