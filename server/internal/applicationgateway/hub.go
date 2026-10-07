package applicationgateway

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ErrOffline means the request has no authenticated runtime data-channel owner.
var ErrOffline = errors.New("application runtime data channel is offline")

type control struct {
	connection *websocket.Conn
	queue      chan protocol.ApplicationTunnelRequest
	closed     chan struct{}
	epoch      string
}

type pendingStream struct {
	runtimeID   string
	workspaceID string
	endpointID  string
	token       string
	epoch       string
	attached    chan *Stream
	revoked     chan struct{}
	stream      *Stream
}

// Hub owns local runtime channels and tears down all streams when an endpoint is revoked.
type Hub struct {
	mu       sync.Mutex
	controls map[string]*control
	streams  map[string]*pendingStream
	cluster  *cluster
}

// NewHub creates an empty broker; registration is authenticated by the handler boundary.
func NewHub() *Hub {
	return &Hub{controls: map[string]*control{}, streams: map[string]*pendingStream{}}
}

func randomToken() (string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(token[:]), nil
}

// Connected reports transport availability separately from application health.
func (h *Hub) Connected(runtimeID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, exists := h.controls[runtimeID]
	return exists
}

// Control serves one runtime's dedicated outbound control connection.
func (h *Hub) Control(ctx context.Context, runtimeID string, connection *websocket.Conn) error {
	epoch, err := randomToken()
	if err != nil {
		return err
	}
	c := &control{connection: connection, queue: make(chan protocol.ApplicationTunnelRequest, 32), closed: make(chan struct{}), epoch: epoch}
	if h.cluster != nil {
		if err := h.cluster.register(ctx, runtimeID, epoch); err != nil {
			connection.Close()
			return err
		}
	}
	h.mu.Lock()
	previous := h.controls[runtimeID]
	h.controls[runtimeID] = c
	h.mu.Unlock()
	if previous != nil {
		previous.connection.Close()
	}
	defer func() {
		if h.cluster != nil {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			if err := h.cluster.release(releaseCtx, runtimeID, epoch); err != nil {
				slog.Debug("application runtime lease will expire after gateway shutdown", "runtime_id", runtimeID)
			}
		}
		connection.Close()
		h.mu.Lock()
		if h.controls[runtimeID] == c {
			delete(h.controls, runtimeID)
		}
		close(c.closed)
		toClose := []*Stream{}
		for _, pending := range h.streams {
			if pending.runtimeID == runtimeID && pending.epoch == epoch && pending.stream != nil {
				toClose = append(toClose, pending.stream)
			}
		}
		h.mu.Unlock()
		for _, stream := range toClose {
			stream.Close()
		}
	}()
	connection.SetReadLimit(1024)
	connection.SetReadDeadline(time.Now().Add(45 * time.Second))
	connection.SetPongHandler(func(string) error { return connection.SetReadDeadline(time.Now().Add(45 * time.Second)) })
	readerDone := make(chan error, 1)
	go func() {
		for {
			if _, _, readErr := connection.ReadMessage(); readErr != nil {
				readerDone <- readErr
				return
			}
		}
	}()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readerDone:
			return err
		case request := <-c.queue:
			if h.cluster != nil {
				owner, err := h.cluster.runtimeOwner(ctx, runtimeID)
				if err != nil || owner.Origin != h.cluster.origin || owner.Epoch != c.epoch {
					return ErrOffline
				}
			}
			connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err = connection.WriteJSON(request); err != nil {
				return err
			}
		case <-ticker.C:
			if h.cluster != nil {
				leaseCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				err := h.cluster.renew(leaseCtx, runtimeID, epoch)
				cancel()
				if err != nil {
					return err
				}
			}
			if err = connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return err
			}
		}
	}
}

// Open asks the owning runtime for an exact, server-authorized endpoint or log stream.
func (h *Hub) Open(ctx context.Context, request protocol.ApplicationTunnelRequest) (net.Conn, error) {
	if h.cluster != nil {
		return h.remoteOpen(ctx, request)
	}
	return h.openLocal(ctx, request)
}

func (h *Hub) openLocal(ctx context.Context, request protocol.ApplicationTunnelRequest) (net.Conn, error) {
	var expectedEpoch string
	if h.cluster != nil {
		owner, err := h.cluster.runtimeOwner(ctx, request.RuntimeID)
		if err != nil {
			return nil, err
		}
		if owner.Origin != h.cluster.origin {
			return nil, ErrOffline
		}
		expectedEpoch = owner.Epoch
	}
	id, err := randomToken()
	if err != nil {
		return nil, err
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	c := h.controls[request.RuntimeID]
	if c == nil || expectedEpoch != "" && c.epoch != expectedEpoch {
		h.mu.Unlock()
		return nil, ErrOffline
	}
	count := 0
	for _, stream := range h.streams {
		if stream.runtimeID == request.RuntimeID {
			count++
		}
	}
	if count >= 128 {
		h.mu.Unlock()
		return nil, errors.New("application runtime stream limit reached")
	}
	pending := &pendingStream{runtimeID: request.RuntimeID, workspaceID: request.WorkspaceID, endpointID: request.EndpointID, token: token, epoch: c.epoch, attached: make(chan *Stream, 1), revoked: make(chan struct{})}
	h.streams[id] = pending
	h.mu.Unlock()
	if h.cluster != nil {
		value, encodeErr := json.Marshal(owner{Origin: h.cluster.origin, Epoch: c.epoch})
		if encodeErr != nil {
			return nil, encodeErr
		}
		if err := h.cluster.redis.Set(ctx, streamOwnerKey(id), value, 20*time.Second).Err(); err != nil {
			h.mu.Lock()
			delete(h.streams, id)
			h.mu.Unlock()
			return nil, err
		}
	}
	request.StreamID = id
	request.Token = token
	remove := func() {
		h.mu.Lock()
		delete(h.streams, id)
		stream := pending.stream
		h.mu.Unlock()
		if stream != nil {
			stream.Close()
		}
	}
	select {
	case c.queue <- request:
	case <-ctx.Done():
		remove()
		return nil, ctx.Err()
	case <-c.closed:
		remove()
		return nil, ErrOffline
	default:
		remove()
		return nil, errors.New("application runtime stream queue is full")
	}
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case stream := <-pending.attached:
		return stream, nil
	case <-ctx.Done():
		remove()
		return nil, ctx.Err()
	case <-c.closed:
		remove()
		return nil, ErrOffline
	case <-timer.C:
		remove()
		return nil, errors.New("application runtime did not attach the requested stream")
	case <-pending.revoked:
		remove()
		return nil, errors.New("application endpoint was revoked")
	}
}

// Attach consumes a one-use stream credential bound to the current control connection epoch.
func (h *Hub) Attach(runtimeID, workspaceID, id, token string, connection *websocket.Conn) (*Stream, error) {
	var expectedEpoch string
	if h.cluster != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		owner, err := h.cluster.runtimeOwner(ctx, runtimeID)
		if err != nil || owner.Origin != h.cluster.origin {
			return nil, ErrOffline
		}
		expectedEpoch = owner.Epoch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	pending := h.streams[id]
	c := h.controls[runtimeID]
	if pending == nil || c == nil || expectedEpoch != "" && c.epoch != expectedEpoch || pending.stream != nil || pending.runtimeID != runtimeID || pending.workspaceID != workspaceID || pending.epoch != c.epoch || len(token) != len(pending.token) || subtle.ConstantTimeCompare([]byte(token), []byte(pending.token)) != 1 {
		return nil, errors.New("application stream credential is invalid or expired")
	}
	stream := NewStream(connection, func() { h.mu.Lock(); delete(h.streams, id); h.mu.Unlock() })
	pending.stream = stream
	pending.token = ""
	pending.attached <- stream
	return stream, nil
}

// Revoke closes attached streams immediately; waiting opens can no longer authenticate.
func (h *Hub) Revoke(endpointID string) {
	h.revokeStreams(endpointID, "")
	h.publishRevocation("endpoint", endpointID)
}

// DisconnectRuntime closes service streams and the removed runtime's control channel.
func (h *Hub) DisconnectRuntime(runtimeID string) {
	h.revokeStreams("", runtimeID)
	h.publishRevocation("runtime", runtimeID)
}

func (h *Hub) revokeStreams(endpointID, runtimeID string) {
	h.mu.Lock()
	var connection *websocket.Conn
	if runtimeID != "" && h.controls[runtimeID] != nil {
		connection = h.controls[runtimeID].connection
	}
	toClose := []*Stream{}
	for id, pending := range h.streams {
		if runtimeID != "" && pending.runtimeID == runtimeID || runtimeID == "" && pending.endpointID == endpointID {
			delete(h.streams, id)
			if pending.revoked != nil {
				close(pending.revoked)
			}
			if pending.stream != nil {
				toClose = append(toClose, pending.stream)
			}
		}
	}
	h.mu.Unlock()
	for _, stream := range toClose {
		stream.Close()
	}
	if connection != nil {
		connection.Close()
	}
}

// Close tears down all local channels when the owning server shuts down.
func (h *Hub) Close() {
	h.mu.Lock()
	connections := make([]*websocket.Conn, 0, len(h.controls))
	for _, control := range h.controls {
		connections = append(connections, control.connection)
	}
	streams := make([]*Stream, 0, len(h.streams))
	for id, pending := range h.streams {
		delete(h.streams, id)
		if pending.revoked != nil {
			close(pending.revoked)
		}
		if pending.stream != nil {
			streams = append(streams, pending.stream)
		}
	}
	h.mu.Unlock()
	for _, stream := range streams {
		stream.Close()
	}
	for _, connection := range connections {
		connection.Close()
	}
}
