package mirror

import (
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// armViewerGrant translates trusted wall-clock expiry once into a monotonic timer.
// Caller holds peer.mu. Renewal timers retain identity to reject stale callbacks.
func (m *RuntimeMirror) armViewerGrant(viewerID string, peer *mirrorPeer) {
	v := peer.grant
	if v.timer != nil {
		v.timer.Stop()
	}
	expiry := v.value.ExpiresAt
	duration := min(time.Until(expiry), 30*time.Second)
	v.deadline = time.Now().Add(duration)
	v.timer = time.AfterFunc(duration, func() {
		_, _ = m.closePeer(viewerID, peer, peerCloseOptions{fromCallback: true, guard: func(p *mirrorPeer) bool { return p.grant.value.ExpiresAt.Equal(expiry) }})
	})
}

// RenewViewerGrant accepts only a strictly newer expiry for the same complete
// grant identity, from a current or newer authenticated control generation.
func (m *RuntimeMirror) RenewViewerGrant(viewerID string, grant protocol.MirrorViewerGrant, generation uint64) bool {
	m.mu.Lock()
	peer := m.peers[viewerID]
	m.mu.Unlock()
	if peer == nil {
		return false
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	v := peer.grant
	if peer.closed || v == nil || !time.Now().Before(v.deadline) || generation < v.generation || !validViewerGrant(viewerID, grant) || !grant.ExpiresAt.After(v.value.ExpiresAt) {
		return false
	}
	old := v.value
	old.ExpiresAt = grant.ExpiresAt
	if old != grant {
		return false
	}
	v.value = grant
	v.generation = generation
	m.armViewerGrant(viewerID, peer)
	return true
}

// RevokeViewerGrant closes only the named grant, never a replacement viewer.
func (m *RuntimeMirror) RevokeViewerGrant(viewerID, grantID string, generation uint64) bool {
	m.mu.Lock()
	peer := m.peers[viewerID]
	m.mu.Unlock()
	if peer == nil {
		return false
	}
	closed, _ := m.closePeer(viewerID, peer, peerCloseOptions{fromCallback: true, guard: func(p *mirrorPeer) bool {
		return p.grant != nil && p.grant.value.GrantID == grantID && generation >= p.grant.generation
	}})
	return closed
}

// viewerGrant is independent of the media protocol: v1 and v2 peers both expire.
type viewerGrant struct {
	value      protocol.MirrorViewerGrant
	generation uint64
	timer      *time.Timer
	deadline   time.Time
}

func validViewerGrant(viewerID string, g protocol.MirrorViewerGrant) bool {
	return g.ViewerID == viewerID && g.GrantID != "" && g.SessionID != "" && g.WorkspaceID != "" && g.RuntimeID != "" && g.UserID != "" && g.NativeEpoch != "" && g.SourceGeneration != "" && g.Source.Validate() == nil && time.Until(g.ExpiresAt) > 0
}

// BindViewerGrant attaches a server-authorized grant before Commit. For JPEG v1,
// the caller must have verified the source is the registry's current primary.
// Existing unbound legacy-capability peers retain their original lifecycle.
func (m *RuntimeMirror) BindViewerGrant(viewerID string, grant protocol.MirrorViewerGrant, generation uint64) bool {
	if !validViewerGrant(viewerID, grant) {
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
	if peer.closed || peer.grant != nil || peer.negotiationCommitted {
		return false
	}
	if peer.video != nil {
		if !validVideoGrant(viewerID, peer.video.source, grant) {
			return false
		}
	} else if grant.Source.Kind == protocol.MirrorSourceVirtual {
		return false
	}
	peer.grant = &viewerGrant{value: grant, generation: generation}
	m.armViewerGrant(viewerID, peer)
	return true
}
