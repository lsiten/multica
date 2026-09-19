package mirror

import (
	"encoding/json"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

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
	if peer.closed || peer.video == nil || peer.video.control == nil {
		return false
	}
	return peer.video.control.SendText(string(payload)) == nil
}
