//go:build darwin

package daemon

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os/exec"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
)

type performanceBoundedOutput struct {
	data  []byte
	limit int
}

func (b *performanceBoundedOutput) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > b.limit {
		return 0, errors.New("route_output_limit")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
func performanceNetworkCommand(ctx context.Context, path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	out := &performanceBoundedOutput{limit: 32768}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	return string(out.data), err
}
func currentPerformanceRoute(ctx context.Context, pair mirror.SelectedICEPair) performanceRouteEvidence {
	missing := performanceRouteEvidence{Kind: "unknown", Method: "Darwin route_get+net.Interfaces+networksetup_hardware_ports", LocalAddress: pair.Local.Address, Destination: pair.Remote.Address}
	address, err := netip.ParseAddr(pair.Remote.Address)
	if err != nil {
		missing.Reason = "selected_address_unavailable"
		return missing
	}
	family := "-inet"
	if address.Is6() {
		family = "-inet6"
	}
	raw, err := performanceNetworkCommand(ctx, "/sbin/route", "-n", "get", family, address.String())
	if err != nil {
		missing.Reason = "kernel_route_readback_unavailable"
		return missing
	}
	route, ok := parsePerformanceRoute(raw)
	if !ok {
		missing.Reason = "kernel_route_shape_unavailable"
		return missing
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		missing.Reason = "interfaces_unavailable"
		return missing
	}
	hardwareText, hardwareErr := performanceNetworkCommand(ctx, "/usr/sbin/networksetup", "-listallhardwareports")
	ports := parsePerformanceHardwarePorts(hardwareText)
	if hardwareErr != nil {
		ports = nil
	}
	var entries []performanceRouteInterface
	for _, i := range interfaces {
		entry := performanceRouteInterface{Name: i.Name, Index: i.Index, Flags: i.Flags, Hardware: i.HardwareAddr.String(), HardwarePort: ports[i.Name][0], HardwarePortMAC: ports[i.Name][1]}
		addresses, err := i.Addrs()
		if err != nil {
			missing.Reason = "interface_addresses_unavailable"
			return missing
		}
		for _, a := range addresses {
			entry.Addresses = append(entry.Addresses, a.String())
		}
		entries = append(entries, entry)
	}
	evidence := classifyPerformanceRoute(pair, route, entries)
	evidence.Method = missing.Method
	return evidence
}
