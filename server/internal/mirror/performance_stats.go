package mirror

import "github.com/pion/webrtc/v4"

// PeerStatistics exposes Pion's own counters for a caller-owned viewer without changing capture.
func (m *RuntimeMirror) PeerStatistics(viewerID string) (webrtc.StatsReport, bool) {
	m.mu.Lock()
	peer := m.peers[viewerID]
	m.mu.Unlock()
	if peer == nil {
		return nil, false
	}
	return peer.pc.GetStats(), true
}
