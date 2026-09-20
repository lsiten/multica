package mirror

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

type pendingAuthorization struct {
	request   protocol.MirrorAuthorizationRequest
	peer      *mirrorPeer
	expiresAt time.Time
}

const maxPendingAuthorizations = 128

// PublishAuthorizationRequest sends a bounded consent prompt to one viewer's
// mirror control channel. The request remains a daemon-local pending decision.
func (m *RuntimeMirror) PublishAuthorizationRequest(viewerID string, request protocol.MirrorAuthorizationRequest) bool {
	if request.Validate(time.Now()) != nil {
		return false
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return false
	}
	m.mu.Lock()
	peer := m.peers[viewerID]
	m.mu.Unlock()
	if peer == nil {
		return false
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.closed || peer.grant == nil || !peer.grant.deadline.After(time.Now()) || peer.video == nil || peer.video.control == nil {
		return false
	}
	m.authorizationMu.Lock()
	for id, pending := range m.pendingAuthorizations {
		if !pending.expiresAt.After(time.Now()) {
			delete(m.pendingAuthorizations, id)
		}
	}
	_, exists := m.pendingAuthorizations[request.RequestID]
	if exists || len(m.pendingAuthorizations) >= maxPendingAuthorizations {
		m.authorizationMu.Unlock()
		return false
	}
	m.pendingAuthorizations[request.RequestID] = pendingAuthorization{peer: peer, expiresAt: request.ExpiresAt, request: request}
	m.authorizationMu.Unlock()
	if err := peer.video.control.SendText(string(payload)); err != nil {
		m.authorizationMu.Lock()
		delete(m.pendingAuthorizations, request.RequestID)
		m.authorizationMu.Unlock()
		return false
	}
	return true
}

// PublishAuthorizationToViewers fans one local authorization prompt out to
// the currently attached viewers. Each viewer receives a distinct request ID
// and therefore can approve only its own prompt.
func (m *RuntimeMirror) PublishAuthorizationToViewers(kind, title, message string) int {
	m.mu.Lock()
	viewerIDs := make([]string, 0, len(m.peers))
	for viewerID := range m.peers {
		viewerIDs = append(viewerIDs, viewerID)
	}
	m.mu.Unlock()
	sent := 0
	for _, viewerID := range viewerIDs {
		if m.publishAuthorizationRequest(viewerID, protocol.MirrorAuthorizationRequest{
			Type: protocol.MirrorAuthorizationRequestType,
			Kind: kind, Title: title, Message: message,
			ExpiresAt: time.Now().Add(2 * time.Minute),
		}) {
			sent++
		}
	}
	return sent
}

func (m *RuntimeMirror) publishAuthorizationRequest(viewerID string, request protocol.MirrorAuthorizationRequest) bool {
	request.RequestID = uuid.NewString()
	return m.PublishAuthorizationRequest(viewerID, request)
}

func (m *RuntimeMirror) consumeAuthorization(peer *mirrorPeer, requestID string, now time.Time) (protocol.MirrorAuthorizationRequest, bool) {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.closed || peer.grant == nil || !peer.grant.deadline.After(now) {
		return protocol.MirrorAuthorizationRequest{}, false
	}
	m.authorizationMu.Lock()
	defer m.authorizationMu.Unlock()
	pending, ok := m.pendingAuthorizations[requestID]
	if !ok || pending.peer != peer {
		return protocol.MirrorAuthorizationRequest{}, false
	}
	delete(m.pendingAuthorizations, requestID)
	return pending.request, pending.expiresAt.After(now)
}

func (m *RuntimeMirror) handleAuthorizationMessage(peer *mirrorPeer, message webrtc.DataChannelMessage) {
	if !message.IsString || len(message.Data) > protocol.MaxMirrorInputBytes {
		return
	}
	var response protocol.MirrorAuthorizationResponse
	if json.Unmarshal(message.Data, &response) != nil || response.Validate() != nil {
		return
	}
	m.mu.Lock()
	handler := m.authorizationHandler
	m.mu.Unlock()
	if handler == nil {
		return
	}
	request, ok := m.consumeAuthorization(peer, response.RequestID, time.Now())
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := handler(ctx, request, response.Approved); err != nil {
		return
	}
}

func (m *RuntimeMirror) releaseAuthorizations(peer *mirrorPeer) {
	m.authorizationMu.Lock()
	defer m.authorizationMu.Unlock()
	for id, pending := range m.pendingAuthorizations {
		if pending.peer == peer {
			delete(m.pendingAuthorizations, id)
		}
	}
}
