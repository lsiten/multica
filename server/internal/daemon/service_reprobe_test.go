package daemon

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// TestReProbeEnvironmentRetiresInProcessBridgeOnRecovery is the regression for
// the double-run guard: once the separate environment process recovers, the
// re-probe must retire the in-process GC/recycle bridge (cancel it) and flip the
// ownership flag so the two never both run the same loops. The re-probe parks on
// a long-lived ctx (it owns the recovered child until the daemon cancels it),
// so the test only drives the handover, not the shutdown close.
func TestReProbeEnvironmentRetiresInProcessBridgeOnRecovery(t *testing.T) {
	d := &Daemon{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	d.probeEnvironmentProcessForTest = func(ctx context.Context) (*environmentProcessClient, error) {
		return &environmentProcessClient{}, nil
	}
	bridgeCtx, bridgeCancel := context.WithCancel(context.Background())
	go d.reProbeEnvironmentService(context.Background(), bridgeCancel)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if d.environmentProcessActive.Load() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !d.environmentProcessActive.Load() {
		t.Fatal("re-probe did not flip the environment ownership flag on recovery")
	}
	select {
	case <-bridgeCtx.Done():
	default:
		t.Fatal("in-process GC/recycle bridge was not retired, so it would double-run the environment service")
	}
	bridgeCancel()
}

// TestReProbeGatewaySwapsInProcessBrokerOnRecovery is the regression for the
// gateway bridge: on recovery the separate gateway becomes the MCP owner seam
// and the ownership flag flips, so the in-process broker is not the permanent
// owner.
func TestReProbeGatewaySwapsInProcessBrokerOnRecovery(t *testing.T) {
	d := &Daemon{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	bridge := &builtinMCPBroker{closed: true}
	d.setMCPBroker(bridge)
	d.probeGatewayProcessForTest = func(ctx context.Context) (*gatewayProcessClient, error) {
		return &gatewayProcessClient{}, nil
	}
	go d.reProbeGatewayService(context.Background())

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if d.gatewayProcessActive.Load() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !d.gatewayProcessActive.Load() {
		t.Fatal("re-probe did not flip the gateway ownership flag on recovery")
	}
	if got := d.mcpBroker(); got == bridge {
		t.Fatal("in-process MCP broker is still the owner after recovery; the separate gateway must own it")
	}
}
