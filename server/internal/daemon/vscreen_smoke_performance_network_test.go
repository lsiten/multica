package daemon

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen/native"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestPerformanceRouteRequiresCurrentPhysicalOnLinkEvidence(t *testing.T) {
	for _, tc := range []string{"lan", "public_on_link", "private_ip_only", "loopback", "same_host", "tunnel", "point_to_point", "relay", "gateway", "foreign_route", "wrong_local", "off_link", "hardware_mismatch", "wrong_device", "unknown_port", "virtual_bridge", "vlan", "unknown_interface", "no_carrier", "down"} {
		t.Run(tc, func(t *testing.T) {
			pair := mirror.SelectedICEPair{State: "succeeded", Nominated: true, Local: mirror.SelectedICEEndpoint{Address: "10.0.0.2", Port: 4000, Protocol: "udp", CandidateType: "host"}, Remote: mirror.SelectedICEEndpoint{Address: "10.0.0.3", Port: 5000, Protocol: "udp", CandidateType: "host"}}
			route := performanceRouteRecord{Interface: "en0", Query: pair.Remote.Address, Destination: pair.Remote.Address, Gateway: "aa:bb:cc:dd:ee:01", Flags: []string{"UP", "HOST", "DONE", "LLINFO"}}
			interfaces := []performanceRouteInterface{{Name: "en0", Index: 4, Flags: net.FlagUp | net.FlagBroadcast | net.FlagRunning, Hardware: "aa:bb:cc:dd:ee:02", HardwarePort: "Wi-Fi", HardwarePortMAC: "aa:bb:cc:dd:ee:02", Addresses: []string{"10.0.0.2/24"}}}
			switch tc {
			case "public_on_link":
				pair.Local.Address = "198.51.100.2"
				pair.Remote.Address = "198.51.100.3"
				route.Query = pair.Remote.Address
				route.Destination = pair.Remote.Address
				interfaces[0].Addresses = []string{"198.51.100.2/24"}
			case "private_ip_only":
				interfaces[0].HardwarePort = ""
			case "loopback":
				pair.Remote.Address = "127.0.0.1"
			case "same_host":
				pair.Remote.Address = pair.Local.Address
			case "tunnel":
				route.Interface = "utun7"
				interfaces[0].Name = "utun7"
			case "point_to_point":
				interfaces[0].Flags |= net.FlagPointToPoint
			case "relay":
				pair.Remote.CandidateType = "relay"
			case "gateway":
				route.Flags = append(route.Flags, "GATEWAY")
			case "foreign_route":
				route.Query = "10.0.0.9"
			case "wrong_local":
				pair.Local.Address = "10.0.0.9"
			case "off_link":
				pair.Remote.Address = "10.0.1.3"
				route.Query = pair.Remote.Address
			case "hardware_mismatch":
				interfaces[0].HardwarePortMAC = "aa:bb:cc:dd:ee:09"
			case "wrong_device":
				route.Interface = "en9"
			case "unknown_port":
				interfaces[0].HardwarePort = "Unknown"
			case "virtual_bridge":
				route.Interface = "bridge0"
				interfaces[0].Name = "bridge0"
			case "vlan":
				route.Interface = "vlan0"
				interfaces[0].Name = "vlan0"
				interfaces[0].HardwarePort = "VLAN"
			case "unknown_interface":
				route.Interface = "fake0"
				interfaces[0].Name = "fake0"
			case "no_carrier":
				interfaces[0].Flags = net.FlagUp | net.FlagBroadcast
			case "down":
				interfaces[0].Flags = net.FlagBroadcast
			}
			got := classifyPerformanceRoute(pair, route, interfaces)
			wantLAN := tc == "lan" || tc == "public_on_link" || tc == "hardware_mismatch"
			if (got.Available && got.Kind == "lan") != wantLAN {
				t.Fatalf("case=%s route=%+v", tc, got)
			}
			if tc == "hardware_mismatch" && (got.InterfaceHardwareAddress != interfaces[0].Hardware || got.HardwarePortAddress != interfaces[0].HardwarePortMAC) {
				t.Fatalf("distinct current and hardware addresses lost: %+v", got)
			}
			if !wantLAN && got.Reason == "" {
				t.Fatal("unqualified topology missing reason")
			}
		})
	}
}
func TestPerformanceRouteParsesOnlyObservedOSFields(t *testing.T) {
	route, ok := parsePerformanceRoute("route to: 10.0.0.3\n destination: 10.0.0.3\n gateway: aa:bb:cc:dd:ee:03\n interface: en0\n flags: <UP,HOST,DONE,LLINFO>\n")
	if !ok || route.Query != "10.0.0.3" || route.Interface != "en0" || route.Flags[3] != "LLINFO" {
		t.Fatal(route, ok)
	}
	ports := parsePerformanceHardwarePorts("Hardware Port: Wi-Fi\nDevice: en0\nEthernet Address: aa:bb:cc:dd:ee:02\n\nHardware Port: Thunderbolt Bridge\nDevice: bridge0\nEthernet Address: aa:bb:cc:dd:ee:04\n")
	if ports["en0"] != [2]string{"Wi-Fi", "aa:bb:cc:dd:ee:02"} || ports["bridge0"][0] != "Thunderbolt Bridge" {
		t.Fatal(ports)
	}
	if _, ok = parsePerformanceRoute("interface: en0\n"); ok {
		t.Fatal("partial route pretended complete")
	}
}
func TestPerformanceNetworkEndpointRejectsForeignScopeAndClosedViewers(t *testing.T) {
	p, _ := performanceTestProducer(t)
	runtime := mirror.NewRuntimeMirror(nil, time.Second)
	p.runtimes["runtime"] = runtime
	source := native.SourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: protocol.ResourceKey{WorkspaceID: p.workspaceID, RuntimeID: "runtime"}, Source: protocol.MirrorSource{Kind: protocol.MirrorSourceVirtual, SourceID: "owned"}, NativeEpoch: "native", Generation: "generation"}}
	p.sources["owned"] = performanceSource{SourceID: "owned", RuntimeID: "runtime", NativeEpoch: "native", Generation: "generation", source: source}
	p.peers["viewer"] = performancePeer{runtime: runtime, sourceID: "owned", grant: protocol.MirrorViewerGrant{GrantID: "grant", ViewerID: "viewer", WorkspaceID: "foreign-workspace", RuntimeID: "runtime", Source: source.Source, NativeEpoch: "native", SourceGeneration: "generation", ExpiresAt: time.Now().Add(time.Minute)}}
	calls := 0
	p.readNetworkRoute = func(context.Context, mirror.SelectedICEPair) performanceRouteEvidence {
		calls++
		return performanceRouteEvidence{Available: true, Kind: "lan"}
	}
	w := performanceTestRequest(p, "GET", "/metrics", nil)
	var response struct {
		Network performanceNetwork `json:"network"`
	}
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Network.Viewers) != 1 {
		t.Fatal(w.Body.String())
	}
	row := response.Network.Viewers[0]
	if row.Available || row.SelectedPair != nil || row.Reason != "private_peer_scope_invalid_or_expired" || calls != 0 {
		t.Fatalf("foreign metadata exposed %+v calls=%d", row, calls)
	}
	delete(p.peers, "viewer")
	w = performanceTestRequest(p, "GET", "/metrics", nil)
	if json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Network.Viewers) != 0 {
		t.Fatal("closed viewer metadata retained")
	}
	if response.Network.RunID != p.clockEpoch {
		t.Fatal("network lost clock/run binding")
	}
}
