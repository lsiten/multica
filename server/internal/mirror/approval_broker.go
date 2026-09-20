package mirror

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// RequestCLIApproval selects one explicitly controlling viewer in this exact
// workspace and runtime. Ambiguous or read-only audiences cannot approve.
func (m *RuntimeMirror) RequestCLIApproval(ctx context.Context, workspaceID, runtimeID, title, message string) (bool, error) {
	m.mu.Lock()
	peers := make(map[string]*mirrorPeer, len(m.peers))
	for id, peer := range m.peers {
		peers[id] = peer
	}
	m.mu.Unlock()
	viewerID := ""
	var selected *controlGrant
	for id, peer := range peers {
		peer.mu.Lock()
		grant := peer.controlGrant
		eligible := !peer.closed && grant != nil && grant.deadline.After(time.Now()) && grant.value.ExpiresAt.After(time.Now()) && grant.value.WorkspaceID == workspaceID && grant.value.RuntimeID == runtimeID
		peer.mu.Unlock()
		if eligible {
			if viewerID != "" {
				return false, errors.New("mirror: ambiguous approval recipient")
			}
			viewerID = id
			selected = grant
		}
	}
	if viewerID == "" {
		return false, errors.New("mirror: no active approval recipient")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	deadline, _ := ctx.Deadline()
	request := protocol.MirrorAuthorizationRequest{Type: protocol.MirrorAuthorizationRequestType, RequestID: uuid.NewString(), Kind: "cli", Title: title, Message: message, ExpiresAt: deadline}
	decision := make(chan bool, 1)
	if !m.publishAuthorizationDecision(viewerID, request, decision, selected) {
		return false, errors.New("mirror: approval delivery failed")
	}
	defer func() {
		m.authorizationMu.Lock()
		delete(m.pendingAuthorizations, request.RequestID)
		m.authorizationMu.Unlock()
	}()
	select {
	case approved := <-decision:
		return approved && ctx.Err() == nil, ctx.Err()
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
