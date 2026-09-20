package mirror

import (
	"context"
	"encoding/json"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/pion/webrtc/v4"
)

type pendingAuthorization struct {
	control   *controlGrant
	decision  chan bool
	request   protocol.MirrorAuthorizationRequest
	peer      *mirrorPeer
	expiresAt time.Time
}

const maxPendingAuthorizations = 128

// PublishAuthorizationRequest sends a bounded consent prompt to one viewer's
// mirror control channel. The request remains a daemon-local pending decision.
func (m *RuntimeMirror) PublishAuthorizationRequest(viewerID string, request protocol.MirrorAuthorizationRequest) bool {
	return m.publishAuthorizationDecision(viewerID, request, nil, nil)
}

func (m *RuntimeMirror) publishAuthorizationDecision(viewerID string, request protocol.MirrorAuthorizationRequest, decision chan bool, expected *controlGrant) bool {
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
	if decision != nil && (expected == nil || peer.controlGrant != expected || !expected.deadline.After(time.Now()) || !expected.value.ExpiresAt.After(time.Now())) {
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
	m.pendingAuthorizations[request.RequestID] = pendingAuthorization{peer: peer, expiresAt: request.ExpiresAt, request: request, decision: decision, control: expected}
	m.authorizationMu.Unlock()
	if err := peer.video.control.SendText(string(payload)); err != nil {
		m.authorizationMu.Lock()
		delete(m.pendingAuthorizations, request.RequestID)
		m.authorizationMu.Unlock()
		return false
	}
	return true
}

func (m *RuntimeMirror) consumeAuthorization(peer *mirrorPeer, requestID string, now time.Time) (pendingAuthorization, bool) {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.closed || peer.grant == nil || !peer.grant.deadline.After(now) {
		return pendingAuthorization{}, false
	}
	m.authorizationMu.Lock()
	defer m.authorizationMu.Unlock()
	pending, ok := m.pendingAuthorizations[requestID]
	if !ok || pending.peer != peer {
		return pendingAuthorization{}, false
	}
	delete(m.pendingAuthorizations, requestID)
	if pending.decision != nil && (peer.controlGrant == nil || peer.controlGrant != pending.control || !peer.controlGrant.deadline.After(now) || !peer.controlGrant.value.ExpiresAt.After(now)) {
		return pendingAuthorization{}, false
	}
	return pending, pending.expiresAt.After(now)
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
	processed := false
	defer func() { m.sendAuthorizationResult(peer, response.RequestID, processed) }()
	request, ok := m.consumeAuthorization(peer, response.RequestID, time.Now())
	if !ok {
		return
	}
	if request.decision != nil {
		select {
		case request.decision <- response.Approved:
			processed = true
		default:
		}
		return
	}
	if handler == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := handler(ctx, request.request, response.Approved); err != nil {
		return
	}
	processed = ctx.Err() == nil
}

func (m *RuntimeMirror) sendAuthorizationResult(peer *mirrorPeer, requestID string, processed bool) {
	payload, err := json.Marshal(struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Processed bool   `json:"processed"`
	}{"mirror-authorization:result", requestID, processed})
	if err != nil {
		return
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.closed || peer.grant == nil || !peer.grant.deadline.After(time.Now()) || peer.video == nil || peer.video.control == nil {
		return
	}
	if err := peer.video.control.SendText(string(payload)); err != nil {
		return
	}
}

func (m *RuntimeMirror) releaseAuthorizations(peer *mirrorPeer) {
	m.authorizationMu.Lock()
	defer m.authorizationMu.Unlock()
	for id, pending := range m.pendingAuthorizations {
		if pending.peer == peer {
			if pending.decision != nil {
				select {
				case pending.decision <- false:
				default:
				}
			}
			delete(m.pendingAuthorizations, id)
		}
	}
}
