package daemon

import (
	"context"
	"time"
)

// The three long-lived split services (environment, application, gateway) share
// one fail-closed bridge contract. At startup the daemon tries the separate
// process; if it cannot start, the daemon keeps the in-process owner running so
// the service stays available. But that bridge must never mask the separate
// process forever: re-probing on a backoff makes the separate process the owner
// again once it is reachable, mirroring the JEV model manager's
// transient-bridge-with-retry design for long-lived loops.
//
// Each re-probe hands over on recovery: the in-process bridge is retired (or the
// owner seam is swapped) so the separate and in-process owners never run the
// same work at once, and the re-probe owns the recovered process until daemon
// shutdown so a child is never orphaned while the daemon is exiting.

// reProbeEnvironmentService re-probes the environment process and, on recovery,
// cancels the in-process GC/recycle bridge. The separate process owns its own
// GC/recycle loops (started on bind), so retiring the in-process bridge is what
// keeps the two from double-running them.
func (d *Daemon) reProbeEnvironmentService(ctx context.Context, bridgeCancel context.CancelFunc) {
	failures := 0
	for {
		delay := reportRetryDelay(failures, nil)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		probeCtx, probeCancel := context.WithTimeout(ctx, 30*time.Second)
		environment, err := d.probeEnvironmentProcess(probeCtx)
		probeCancel()
		if err != nil {
			failures++
			d.logger.Warn("environment service re-probe failed; in-process bridge retained", "error", err)
			continue
		}
		d.environmentProcessActive.Store(true)
		bridgeCancel()
		d.logger.Info("environment service recovered; in-process GC/recycle bridge retired")
		<-ctx.Done()
		if cerr := environment.close(); cerr != nil {
			d.logger.Warn("environment service shutdown unconfirmed", "error", cerr)
		}
		return
	}
}

// reProbeApplicationService re-probes the application process and, on recovery,
// cancels the in-process application/tunnel bridge and starts the process loop.
// The process loop reconciles the same application registry the in-process
// loops used, so retiring the bridge avoids double-driving the same runtimes.
func (d *Daemon) reProbeApplicationService(ctx context.Context, bridgeCancel context.CancelFunc) {
	failures := 0
	for {
		delay := reportRetryDelay(failures, nil)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		probeCtx, probeCancel := context.WithTimeout(ctx, 30*time.Second)
		client, err := d.probeApplicationProcess(probeCtx)
		probeCancel()
		if err != nil {
			failures++
			d.logger.Warn("application service re-probe failed; in-process bridge retained", "error", err)
			continue
		}
		d.applicationProcessActive.Store(true)
		bridgeCancel()
		d.logger.Info("application service recovered; in-process bridge retired")
		loopDone := make(chan struct{})
		go func() {
			defer close(loopDone)
			d.applicationProcessLoop(ctx, client)
		}()
		<-ctx.Done()
		select {
		case <-loopDone:
		case <-time.After(30 * time.Second):
		}
		if err := client.close(); err != nil {
			d.logger.Warn("application service shutdown unconfirmed", "error", err)
		}
		// The in-process bridge may have owned applications before handover;
		// stop them at daemon shutdown, matching the config-off path.
		d.stopOwnedApplications()
		return
	}
}

// reProbeGatewayService re-probes the gateway process and, on recovery, swaps the
// in-process MCP broker for the separate process. The in-process broker keeps
// running until daemon shutdown (it is a full listener, not a hollow shell), so
// a task that captured it before the swap still drains on it rather than a torn
// owner; the swap is the only point where new tasks see the separate gateway.
func (d *Daemon) reProbeGatewayService(ctx context.Context) {
	failures := 0
	for {
		delay := reportRetryDelay(failures, nil)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		probeCtx, probeCancel := context.WithTimeout(ctx, 30*time.Second)
		gateway, err := d.probeGatewayProcess(probeCtx)
		probeCancel()
		if err != nil {
			failures++
			d.logger.Warn("gateway service re-probe failed; in-process MCP broker retained", "error", err)
			continue
		}
		d.gatewayProcessActive.Store(true)
		d.setMCPBroker(gateway)
		d.logger.Info("gateway service recovered; in-process MCP broker retired")
		<-ctx.Done()
		d.retainGatewayOnRetainedRestart(gateway)
		d.setMCPBroker(nil)
		return
	}
}

// probe*Process are the re-probe's injectable entry points. They default to the
// real ensure*Process; a test may set the *ForTest hook to a fake so the
// handover (bridge retirement, flag, owner seam) is exercised without a real
// child process.
func (d *Daemon) probeEnvironmentProcess(ctx context.Context) (*environmentProcessClient, error) {
	if d.probeEnvironmentProcessForTest != nil {
		return d.probeEnvironmentProcessForTest(ctx)
	}
	return d.ensureEnvironmentProcess(ctx)
}

func (d *Daemon) probeApplicationProcess(ctx context.Context) (*applicationProcessClient, error) {
	if d.probeApplicationProcessForTest != nil {
		return d.probeApplicationProcessForTest(ctx)
	}
	return d.ensureApplicationProcess(ctx)
}

func (d *Daemon) probeGatewayProcess(ctx context.Context) (*gatewayProcessClient, error) {
	if d.probeGatewayProcessForTest != nil {
		return d.probeGatewayProcessForTest(ctx)
	}
	return d.ensureGatewayProcess(ctx)
}
