package daemon

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
)

type performanceRouteEvidence struct {
	Available                bool     `json:"available"`
	Reason                   string   `json:"reason,omitempty"`
	InterfaceName            string   `json:"interface_name,omitempty"`
	InterfaceIndex           int      `json:"interface_index,omitempty"`
	InterfaceFlags           []string `json:"interface_flags,omitempty"`
	InterfaceAddresses       []string `json:"interface_addresses,omitempty"`
	InterfaceHardwareAddress string   `json:"interface_hardware_address,omitempty"`
	HardwarePort             string   `json:"hardware_port,omitempty"`
	HardwarePortAddress      string   `json:"hardware_port_address,omitempty"`
	RouteFlags               []string `json:"route_flags,omitempty"`
	Kind                     string   `json:"kind"`
	LocalAddress             string   `json:"local_address,omitempty"`
	Destination              string   `json:"destination,omitempty"`
	Gateway                  string   `json:"gateway,omitempty"`
	Method                   string   `json:"method"`
}
type performanceNetworkViewer struct {
	ViewerID       string `json:"viewer_id"`
	SourceID       string `json:"source_id"`
	GrantID        string `json:"grant_id"`
	ObservedHostNS string `json:"observed_host_ns"`
	mirror.PeerNetworkObservation
	Route performanceRouteEvidence `json:"route"`
}
type performanceNetwork struct {
	SchemaVersion int                        `json:"schema_version"`
	RunID         string                     `json:"run_id"`
	Viewers       []performanceNetworkViewer `json:"viewers"`
}

// Caller holds the producer lock, so private viewer/source/grant replacement cannot race this read.
func (p *performanceProducer) networkObservation(ctx context.Context) performanceNetwork {
	out := performanceNetwork{SchemaVersion: 1, RunID: p.clockEpoch, Viewers: []performanceNetworkViewer{}}
	ids := make([]string, 0, len(p.peers))
	for id := range p.peers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		peer := p.peers[id]
		row := performanceNetworkViewer{ViewerID: id, SourceID: peer.sourceID, GrantID: peer.grant.GrantID, ObservedHostNS: strconv.FormatUint(p.now(), 10), Route: performanceRouteEvidence{Kind: "unknown", Method: "not_queried", Reason: "selected_pair_unavailable"}}
		source, ok := p.sources[peer.sourceID]
		if !ok || peer.runtime == nil || p.runtimes[source.RuntimeID] != peer.runtime || peer.grant.ViewerID != id || peer.grant.Source != source.source.Source || peer.grant.RuntimeID != source.RuntimeID || peer.grant.WorkspaceID != source.source.Resource.WorkspaceID || peer.grant.NativeEpoch != source.NativeEpoch || peer.grant.SourceGeneration != source.Generation || !time.Now().Before(peer.grant.ExpiresAt) {
			row.Reason = "private_peer_scope_invalid_or_expired"
			out.Viewers = append(out.Viewers, row)
			continue
		}
		row.PeerNetworkObservation = peer.runtime.PeerNetworkObservation(id)
		if row.Available && row.SelectedPair != nil {
			readRoute := p.readNetworkRoute
			if readRoute == nil {
				readRoute = currentPerformanceRoute
			}
			row.Route = readRoute(ctx, *row.SelectedPair)
			after := peer.runtime.PeerNetworkObservation(id)
			if !time.Now().Before(peer.grant.ExpiresAt) || !after.Available || after.SelectedPair == nil || !samePerformancePair(*row.SelectedPair, *after.SelectedPair) || row.DTLS != after.DTLS {
				row.PeerNetworkObservation = mirror.PeerNetworkObservation{Reason: "selected_peer_changed_during_route_read"}
				row.Route = performanceRouteEvidence{Kind: "unknown", Method: "discarded_stale_readback", Reason: "selected_peer_changed_during_route_read"}
			} else {
				row.PeerNetworkObservation = after
			}
		}
		out.Viewers = append(out.Viewers, row)
	}
	return out
}
func samePerformancePair(a, b mirror.SelectedICEPair) bool {
	return a.PairID == b.PairID && a.Local == b.Local && a.Remote == b.Remote && a.State == b.State && a.Nominated == b.Nominated && b.BytesSent >= a.BytesSent && b.BytesReceived >= a.BytesReceived
}
