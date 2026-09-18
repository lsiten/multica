package daemon

import (
	"net"
	"net/netip"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/internal/mirror"
)

type performanceRouteRecord struct {
	Interface, Destination, Gateway, Query string
	Flags                                  []string
}
type performanceRouteInterface struct {
	Name            string
	Index           int
	Flags           net.Flags
	Hardware        string
	Addresses       []string
	HardwarePort    string
	HardwarePortMAC string
}

func classifyPerformanceRoute(pair mirror.SelectedICEPair, route performanceRouteRecord, interfaces []performanceRouteInterface) performanceRouteEvidence {
	e := performanceRouteEvidence{Kind: "unknown", Method: "kernel_route+current_interfaces+OS_hardware_ports", LocalAddress: pair.Local.Address, Destination: pair.Remote.Address, Gateway: route.Gateway, RouteFlags: route.Flags, InterfaceName: route.Interface}
	reject := func(reason string) performanceRouteEvidence { e.Reason = reason; return e }
	local, le := netip.ParseAddr(pair.Local.Address)
	remote, re := netip.ParseAddr(pair.Remote.Address)
	if le != nil || re != nil || !local.IsValid() || !remote.IsValid() {
		return reject("selected_address_unavailable")
	}
	local = local.Unmap()
	remote = remote.Unmap()
	if pair.State != "succeeded" || !pair.Nominated {
		return reject("selected_pair_not_nominated")
	}
	if pair.Local.CandidateType != "host" || pair.Remote.CandidateType != "host" {
		return reject("relay_or_non_host_candidate")
	}
	if pair.Local.Protocol != "udp" || pair.Remote.Protocol != "udp" {
		return reject("transport_not_qualified")
	}
	var selected *performanceRouteInterface
	self := false
	for i := range interfaces {
		entry := &interfaces[i]
		if entry.Name == route.Interface {
			selected = entry
		}
		for _, address := range entry.Addresses {
			prefix, err := netip.ParsePrefix(address)
			if err == nil && prefix.Addr().Unmap() == remote {
				self = true
			}
		}
	}
	if selected != nil {
		e.InterfaceIndex = selected.Index
		e.InterfaceAddresses = selected.Addresses
		e.InterfaceHardwareAddress = selected.Hardware
		e.HardwarePort = selected.HardwarePort
		e.HardwarePortAddress = selected.HardwarePortMAC
		for flag, name := range map[net.Flags]string{net.FlagUp: "up", net.FlagBroadcast: "broadcast", net.FlagLoopback: "loopback", net.FlagPointToPoint: "pointtopoint", net.FlagMulticast: "multicast", net.FlagRunning: "running"} {
			if selected.Flags&flag != 0 {
				e.InterfaceFlags = append(e.InterfaceFlags, name)
			}
		}
	}
	if local.IsLoopback() || remote.IsLoopback() || self || selected != nil && selected.Flags&net.FlagLoopback != 0 {
		e.Available = true
		e.Kind = "loopback"
		e.Reason = "same_host_or_loopback_route"
		return e
	}
	if selected == nil {
		return reject("route_interface_unavailable")
	}
	lower := strings.ToLower(selected.Name)
	for _, prefix := range []string{"utun", "tun", "tap", "ppp", "ipsec", "wg", "tailscale"} {
		if strings.HasPrefix(lower, prefix) {
			e.Available = true
			e.Kind = "tunnel"
			e.Reason = "tunnel_interface"
			return e
		}
	}
	if selected.Flags&net.FlagPointToPoint != 0 {
		e.Available = true
		e.Kind = "tunnel"
		e.Reason = "point_to_point_interface"
		return e
	}
	if selected.Flags&net.FlagUp == 0 || selected.Flags&net.FlagBroadcast == 0 || selected.Flags&net.FlagRunning == 0 {
		return reject("interface_not_active_broadcast")
	}
	routeUp := false
	for _, flag := range route.Flags {
		flag = strings.ToUpper(strings.TrimSpace(flag))
		if flag == "UP" {
			routeUp = true
		}
		switch flag {
		case "GATEWAY", "REJECT", "BLACKHOLE":
			return reject("routed_or_rejected_topology_not_proven_lan")
		}
	}
	if !routeUp {
		return reject("route_not_up")
	}
	query, queryErr := netip.ParseAddr(route.Query)
	if queryErr != nil || query.Unmap() != remote {
		return reject("route_destination_mismatch")
	}
	for _, prefix := range []string{"bridge", "vmnet", "vbox", "docker", "awdl", "llw", "vlan", "veth", "vnic", "gif", "stf"} {
		if strings.HasPrefix(lower, prefix) {
			return reject("virtual_or_peer_interface_not_qualified")
		}
	}
	if !strings.HasPrefix(lower, "en") || len(lower) < 3 || strings.Trim(lower[2:], "0123456789") != "" {
		return reject("physical_interface_kind_unconfirmed")
	}
	hardware, err := net.ParseMAC(selected.Hardware)
	reported, otherErr := net.ParseMAC(selected.HardwarePortMAC)
	port := strings.ToLower(selected.HardwarePort)
	physicalPort := strings.Contains(port, "ethernet") || strings.Contains(port, "wi-fi") || strings.Contains(port, "wifi") || strings.Contains(port, "lan")
	if err != nil || otherErr != nil || len(hardware) != 6 || len(reported) != 6 || !physicalPort || strings.Contains(port, "bridge") || strings.Contains(port, "virtual") || strings.Contains(port, "vpn") {
		return reject("physical_hardware_port_unconfirmed")
	}
	ownsLocal, onLink := false, false
	for _, address := range selected.Addresses {
		prefix, err := netip.ParsePrefix(address)
		if err != nil {
			continue
		}
		if prefix.Addr().Unmap() == local {
			ownsLocal = true
			if prefix.Contains(remote) {
				onLink = true
			}
		}
	}
	if !ownsLocal {
		return reject("selected_local_not_on_route_interface")
	}
	if !onLink {
		return reject("remote_not_on_selected_interface_prefix")
	}
	if !remote.IsGlobalUnicast() || remote.IsUnspecified() {
		return reject("remote_address_not_unicast")
	}
	sort.Strings(e.InterfaceFlags)
	sort.Strings(e.InterfaceAddresses)
	e.Available = true
	e.Kind = "lan"
	return e
}
func parsePerformanceRoute(text string) (performanceRouteRecord, bool) {
	var r performanceRouteRecord
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "route to":
			r.Query = value
		case "interface":
			r.Interface = value
		case "destination":
			r.Destination = value
		case "gateway":
			r.Gateway = value
		case "flags":
			r.Flags = strings.Split(strings.Trim(value, "<>"), ",")
		}
	}
	return r, r.Interface != "" && r.Destination != "" && len(r.Flags) > 0
}
func parsePerformanceHardwarePorts(text string) map[string][2]string {
	ports := map[string][2]string{}
	name, device := "", ""
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "Hardware Port":
			name = value
			device = ""
		case "Device":
			device = value
		case "Ethernet Address":
			if name != "" && device != "" {
				if _, exists := ports[device]; exists {
					ports[device] = [2]string{}
				} else {
					ports[device] = [2]string{name, value}
				}
			}
		}
	}
	return ports
}
