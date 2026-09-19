package mirror

import (
	"sort"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// BindControlGrant attaches a server-authorized input capability to an
// existing peer. A later expiry for the same capability preserves replay state;
// closed peers and grants belonging to another viewer are rejected.
func (m *RuntimeMirror) BindControlGrant(viewerID string, grant protocol.MirrorControlGrant, generation uint64) bool {
	if !validControlGrant(viewerID, grant) {
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
	if peer.closed {
		return false
	}
	if current := peer.controlGrant; current != nil {
		if current.value.EqualIdentity(grant) {
			if !time.Now().Before(current.deadline) || generation < current.generation || !grant.ExpiresAt.After(current.value.ExpiresAt) {
				return false
			}
			current.value = grant
			current.generation = generation
			m.armControlGrant(viewerID, peer)
			return true
		} else {
			m.dropControlGrantLocked(viewerID, peer)
		}
	}
	peer.controlGrant = &controlGrant{value: grant, generation: generation}
	m.armControlGrant(viewerID, peer)
	m.notifyControlState(viewerID, grant.UserID, true, grant.Source)
	return true
}

// RenewControlGrant accepts a strictly later expiry for the same grant.
func (m *RuntimeMirror) RenewControlGrant(viewerID string, grant protocol.MirrorControlGrant, generation uint64) bool {
	m.mu.Lock()
	peer := m.peers[viewerID]
	m.mu.Unlock()
	if peer == nil {
		return false
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	c := peer.controlGrant
	if peer.closed || c == nil || !time.Now().Before(c.deadline) || generation < c.generation {
		return false
	}
	if !c.value.EqualIdentity(grant) || !grant.ExpiresAt.After(c.value.ExpiresAt) || !validControlGrant(viewerID, grant) {
		return false
	}
	c.value = grant
	c.generation = generation
	m.armControlGrant(viewerID, peer)
	return true
}

// ReplaceControlGrant atomically moves a viewer from one bound source to
// another. The old capability is dropped before the new one is armed, preventing
// a viewer from controlling two displays while switching sources.
func (m *RuntimeMirror) ReplaceControlGrant(viewerID string, grant protocol.MirrorControlGrant, generation uint64) bool {
	m.mu.Lock()
	peer := m.peers[viewerID]
	m.mu.Unlock()
	if peer == nil {
		return false
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	c := peer.controlGrant
	if peer.closed || c == nil || !time.Now().Before(c.deadline) || generation < c.generation {
		return false
	}
	if sameControlSource(c.value, grant) || !grant.ExpiresAt.After(c.value.ExpiresAt) || !validControlGrant(viewerID, grant) {
		return false
	}
	m.dropControlGrantLockedNotify(viewerID, peer, false)
	peer.controlGrant = &controlGrant{value: grant, generation: generation}
	m.armControlGrant(viewerID, peer)
	m.notifyControlState(viewerID, grant.UserID, true, grant.Source)
	return true
}

// CurrentControlGrant returns the grant currently bound to a viewer.
func (m *RuntimeMirror) CurrentControlGrant(viewerID string) (protocol.MirrorControlGrant, bool) {
	m.mu.Lock()
	peer := m.peers[viewerID]
	m.mu.Unlock()
	if peer == nil {
		return protocol.MirrorControlGrant{}, false
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.controlGrant == nil || !time.Now().Before(peer.controlGrant.deadline) {
		return protocol.MirrorControlGrant{}, false
	}
	return peer.controlGrant.value, true
}

// RevokeControlGrant closes the input capability but leaves the viewing
// connection intact: revoking control returns the viewer to read-only.
func (m *RuntimeMirror) RevokeControlGrant(viewerID, grantID string, generation uint64) bool {
	m.mu.Lock()
	peer := m.peers[viewerID]
	m.mu.Unlock()
	if peer == nil {
		return false
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	c := peer.controlGrant
	if c == nil || c.value.GrantID != grantID || generation < c.generation {
		return false
	}
	m.dropControlGrantLocked(viewerID, peer)
	return true
}

// HasControl reports whether the viewer currently holds an input capability.
func (m *RuntimeMirror) HasControl(viewerID string) bool {
	m.mu.Lock()
	peer := m.peers[viewerID]
	m.mu.Unlock()
	if peer == nil {
		return false
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	return peer.controlGrant != nil && time.Now().Before(peer.controlGrant.deadline)
}

func (m *RuntimeMirror) armControlGrant(viewerID string, peer *mirrorPeer) {
	c := peer.controlGrant
	if c == nil {
		return
	}
	if c.timer != nil {
		c.timer.Stop()
	}
	duration := time.Until(c.value.ExpiresAt)
	if duration > controlGrantTTL {
		duration = controlGrantTTL
	}
	c.deadline = time.Now().Add(duration)
	expiry := c.value.ExpiresAt
	c.timer = time.AfterFunc(duration, func() {
		peer.mu.Lock()
		if peer.controlGrant != nil && peer.controlGrant.value.ExpiresAt.Equal(expiry) {
			m.dropControlGrantLocked(viewerID, peer)
		}
		peer.mu.Unlock()
	})
}

func (m *RuntimeMirror) dropControlGrantLocked(viewerID string, peer *mirrorPeer) {
	m.dropControlGrantLockedNotify(viewerID, peer, true)
}

func (m *RuntimeMirror) dropControlGrantLockedNotify(viewerID string, peer *mirrorPeer, notify bool) {
	if peer.controlGrant == nil {
		return
	}
	if peer.controlGrant.timer != nil {
		peer.controlGrant.timer.Stop()
	}
	grant := peer.controlGrant.value
	peer.controlGrant = nil
	if peer.inputClose != nil {
		peer.inputClose()
		peer.inputClose = nil
	}
	if a := m.Arbiter(); a != nil {
		a.ReleasePrincipal(Principal{Kind: PrincipalHuman, ID: viewerID})
	}
	if notify {
		m.notifyControlState(viewerID, grant.UserID, false, grant.Source)
	}
}

func (m *RuntimeMirror) notifyControlState(viewerID, userID string, active bool, source protocol.MirrorSource) {
	if hook := m.controlStateHook(); hook != nil {
		hook(ControlStateChange{ViewerID: viewerID, UserID: userID, Active: active, Source: source})
	}
}

// ControlStateChange reports a viewer gaining or losing the input capability.
type ControlStateChange struct {
	ViewerID string
	UserID   string
	Active   bool
	Source   protocol.MirrorSource
}

func (m *RuntimeMirror) controlStateHook() func(ControlStateChange) {
	m.controlMu.Lock()
	defer m.controlMu.Unlock()
	return m.controlStateHookFn
}

// SetControlStateHook installs the observer for control transitions and replays
// every currently bound grant. Hooks from an older daemon control connection
// cannot replace a newer hook, preventing reconnect reordering.
func (m *RuntimeMirror) SetControlStateHook(fn func(ControlStateChange), generation uint64) bool {
	m.controlMu.Lock()
	if m.controlStateHookGeneration != 0 && generation < m.controlStateHookGeneration {
		m.controlMu.Unlock()
		return false
	}
	m.controlStateHookGeneration = generation
	m.controlStateHookFn = fn
	m.controlMu.Unlock()

	if fn == nil {
		return true
	}
	m.mu.Lock()
	viewerIDs := make([]string, 0, len(m.peers))
	for viewerID := range m.peers {
		viewerIDs = append(viewerIDs, viewerID)
	}
	sort.Strings(viewerIDs)
	type boundControl struct {
		viewerID string
		grant    protocol.MirrorControlGrant
	}
	bound := make([]boundControl, 0, len(viewerIDs))
	for _, viewerID := range viewerIDs {
		peer := m.peers[viewerID]
		peer.mu.Lock()
		if peer.controlGrant != nil {
			bound = append(bound, boundControl{viewerID: viewerID, grant: peer.controlGrant.value})
		}
		peer.mu.Unlock()
	}
	m.mu.Unlock()
	for _, item := range bound {
		fn(ControlStateChange{ViewerID: item.viewerID, UserID: item.grant.UserID, Active: true, Source: item.grant.Source})
	}
	return true
}

// RevokeAllControl immediately returns every viewer on this mirror to
// read-only. It is used by the host emergency-stop path; viewing connections
// stay open.
func (m *RuntimeMirror) RevokeAllControl(generation uint64) []protocol.MirrorControlGrant {
	m.mu.Lock()
	viewers := make([]string, 0, len(m.peers))
	for viewerID := range m.peers {
		viewers = append(viewers, viewerID)
	}
	m.mu.Unlock()

	revoked := make([]protocol.MirrorControlGrant, 0)
	for _, viewerID := range viewers {
		m.mu.Lock()
		peer := m.peers[viewerID]
		m.mu.Unlock()
		if peer == nil {
			continue
		}
		peer.mu.Lock()
		current := peer.controlGrant
		if peer.closed || current == nil || (generation != 0 && current.generation != generation) {
			peer.mu.Unlock()
			continue
		}
		grant := current.value
		m.dropControlGrantLocked(viewerID, peer)
		peer.mu.Unlock()
		revoked = append(revoked, grant)
	}
	return revoked
}
