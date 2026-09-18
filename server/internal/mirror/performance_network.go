package mirror

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"net/netip"
	"strings"

	"github.com/pion/webrtc/v4"
)

// SelectedICEEndpoint is read from the selected transport, never the SDP candidate list.
type SelectedICEEndpoint struct {
	Address       string `json:"address"`
	Port          uint16 `json:"port"`
	Protocol      string `json:"protocol"`
	CandidateType string `json:"candidate_type"`
}

// SelectedICEPair identifies one currently nominated transport and its native Pion counters.
type SelectedICEPair struct {
	PairID        string              `json:"pair_id"`
	State         string              `json:"state"`
	Nominated     bool                `json:"nominated"`
	Local         SelectedICEEndpoint `json:"local"`
	Remote        SelectedICEEndpoint `json:"remote"`
	BytesSent     uint64              `json:"bytes_sent"`
	BytesReceived uint64              `json:"bytes_received"`
}

// PeerDTLSIdentity contains fingerprints of the actual connected DTLS certificates.
type PeerDTLSIdentity struct {
	Available         bool   `json:"available"`
	Reason            string `json:"reason,omitempty"`
	Algorithm         string `json:"algorithm,omitempty"`
	LocalFingerprint  string `json:"local_fingerprint,omitempty"`
	RemoteFingerprint string `json:"remote_fingerprint,omitempty"`
}

// PeerNetworkObservation is an instantaneous, fail-closed readback of an owned peer.
type PeerNetworkObservation struct {
	Available    bool             `json:"available"`
	Reason       string           `json:"reason,omitempty"`
	SelectedPair *SelectedICEPair `json:"selected_pair,omitempty"`
	DTLS         PeerDTLSIdentity `json:"dtls"`
}

// PeerNetworkObservation reads the named viewer's actual video sender transport and rechecks its incarnation.
func (m *RuntimeMirror) PeerNetworkObservation(viewerID string) PeerNetworkObservation {
	m.mu.Lock()
	peer := m.peers[viewerID]
	m.mu.Unlock()
	if peer == nil {
		return PeerNetworkObservation{Reason: "peer_unavailable"}
	}
	peer.mu.Lock()
	closed := peer.closed
	var sender *webrtc.RTPSender
	if peer.video != nil {
		sender = peer.video.sender
	}
	peer.mu.Unlock()
	if closed || peer.pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
		return PeerNetworkObservation{Reason: "peer_not_connected"}
	}
	if sender == nil {
		return PeerNetworkObservation{Reason: "video_transport_unavailable"}
	}
	dtls := sender.Transport()
	if dtls == nil || dtls.State() != webrtc.DTLSTransportStateConnected {
		return PeerNetworkObservation{Reason: "dtls_not_connected"}
	}
	transport := dtls.ICETransport()
	if transport == nil {
		return PeerNetworkObservation{Reason: "ice_transport_unavailable"}
	}
	before, err := transport.GetSelectedCandidatePair()
	if err != nil || before == nil {
		return PeerNetworkObservation{Reason: "selected_pair_unavailable"}
	}
	stats, ok := transport.GetSelectedCandidatePairStats()
	if !ok {
		return PeerNetworkObservation{Reason: "selected_pair_stats_unavailable"}
	}
	report := peer.pc.GetStats()
	after, err := transport.GetSelectedCandidatePair()
	if err != nil || after == nil {
		return PeerNetworkObservation{Reason: "selected_pair_changed"}
	}
	afterStats, ok := transport.GetSelectedCandidatePairStats()
	if !ok {
		return PeerNetworkObservation{Reason: "selected_pair_stats_unavailable"}
	}
	pair, reason := stableSelectedPair(before, stats, report, after, afterStats)
	if reason != "" {
		return PeerNetworkObservation{Reason: reason}
	}
	identity := readPeerDTLSIdentity(dtls)
	if !identity.Available {
		return PeerNetworkObservation{Reason: identity.Reason, DTLS: identity}
	}
	m.mu.Lock()
	current := m.peers[viewerID] == peer
	m.mu.Unlock()
	peer.mu.Lock()
	closed = peer.closed
	peer.mu.Unlock()
	if !current || closed || peer.pc.ConnectionState() != webrtc.PeerConnectionStateConnected || dtls.State() != webrtc.DTLSTransportStateConnected {
		return PeerNetworkObservation{Reason: "peer_closed_or_replaced"}
	}
	return PeerNetworkObservation{Available: true, SelectedPair: pair, DTLS: identity}
}
func selectedEndpoint(c *webrtc.ICECandidate) (SelectedICEEndpoint, bool) {
	if c == nil || c.Port == 0 {
		return SelectedICEEndpoint{}, false
	}
	address, err := netip.ParseAddr(c.Address)
	if err != nil {
		return SelectedICEEndpoint{}, false
	}
	return SelectedICEEndpoint{Address: address.Unmap().String(), Port: c.Port, Protocol: c.Protocol.String(), CandidateType: c.Typ.String()}, true
}
func stableSelectedPair(before *webrtc.ICECandidatePair, stats webrtc.ICECandidatePairStats, report webrtc.StatsReport, after *webrtc.ICECandidatePair, afterStats webrtc.ICECandidatePairStats) (*SelectedICEPair, string) {
	if before == nil || after == nil || stats.ID == "" || stats.LocalCandidateID == "" || stats.RemoteCandidateID == "" || stats.LocalCandidateID == stats.RemoteCandidateID || stats.ID != afterStats.ID || stats.LocalCandidateID != afterStats.LocalCandidateID || stats.RemoteCandidateID != afterStats.RemoteCandidateID || stats.State != webrtc.StatsICECandidatePairStateSucceeded || afterStats.State != stats.State || !stats.Nominated || !afterStats.Nominated || afterStats.BytesSent < stats.BytesSent || afterStats.BytesReceived < stats.BytesReceived {
		return nil, "selected_pair_unstable_or_not_nominated"
	}
	local, ok := selectedEndpoint(before.Local)
	if !ok {
		return nil, "selected_address_unavailable"
	}
	remote, ok := selectedEndpoint(before.Remote)
	if !ok {
		return nil, "selected_address_unavailable"
	}
	lastLocal, a := selectedEndpoint(after.Local)
	lastRemote, b := selectedEndpoint(after.Remote)
	if !a || !b || local != lastLocal || remote != lastRemote {
		return nil, "selected_pair_changed"
	}
	for id, endpoint := range map[string]SelectedICEEndpoint{stats.LocalCandidateID: local, stats.RemoteCandidateID: remote} {
		candidate, ok := report[id].(webrtc.ICECandidateStats)
		if !ok || candidate.Deleted || normalizedICEAddress(candidate.IP) != endpoint.Address || candidate.Port != int32(endpoint.Port) || candidate.Protocol != endpoint.Protocol || candidate.CandidateType.String() != endpoint.CandidateType {
			return nil, "selected_candidate_stats_mismatch"
		}
	}
	return &SelectedICEPair{PairID: stats.ID, State: string(stats.State), Nominated: stats.Nominated, Local: local, Remote: remote, BytesSent: afterStats.BytesSent, BytesReceived: afterStats.BytesReceived}, ""
}
func readPeerDTLSIdentity(transport *webrtc.DTLSTransport) PeerDTLSIdentity {
	parameters, err := transport.GetLocalParameters()
	if err != nil {
		return PeerDTLSIdentity{Reason: "local_certificate_unavailable"}
	}
	local := ""
	for _, f := range parameters.Fingerprints {
		if strings.EqualFold(f.Algorithm, "sha-256") {
			value, err := hex.DecodeString(strings.ReplaceAll(f.Value, ":", ""))
			if err != nil || len(value) != sha256.Size {
				return PeerDTLSIdentity{Reason: "local_fingerprint_invalid"}
			}
			normalized := hex.EncodeToString(value)
			if local != "" && local != normalized {
				return PeerDTLSIdentity{Reason: "local_certificate_ambiguous"}
			}
			local = normalized
		}
	}
	raw := append([]byte(nil), transport.GetRemoteCertificate()...)
	certificate, err := x509.ParseCertificate(raw)
	if local == "" || err != nil {
		return PeerDTLSIdentity{Reason: "connected_certificate_unavailable"}
	}
	remote := sha256.Sum256(certificate.Raw)
	return PeerDTLSIdentity{Available: true, Algorithm: "sha-256", LocalFingerprint: local, RemoteFingerprint: hex.EncodeToString(remote[:])}
}

func normalizedICEAddress(value string) string {
	address, err := netip.ParseAddr(value)
	if err != nil {
		return ""
	}
	return address.Unmap().String()
}
