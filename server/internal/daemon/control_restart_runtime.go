package daemon

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// RunControlService runs the opt-in control record service. It is the child-side
// entrypoint for the control runtime service (see the TestMain control branch)
// and is also the mechanism the daemon uses, opt-in, to own its own control
// record. It is intentionally minimal: the control is the supervisor, not a
// managed child, so it registers no domain capabilities — it only needs the
// transport lifecycle to publish a durable receipt and, on a clean stop, to
// transition its record to "stopped". That "stopped" record is what a successor
// control reads (via runtimeproc.InspectRecord in startControlRuntime) to confirm
// the prior instance stopped and admit a capability-replacing restart while
// retaining an execution. A NewService failure is returned so the caller can keep
// drain-before-restart; it never blocks startup.
func RunControlService(ctx context.Context, b runtimeproc.Bootstrap) error {
	service, err := runtimeproc.NewService(runtimeproc.Config{Bootstrap: b})
	if err != nil {
		return err
	}
	return service.Serve(ctx)
}

// startControlRuntime opt-ins the control (this daemon) into owning a runtimeproc
// record so a capability-replacing control restart can confirm this instance
// stopped and retain an execution (F3/G). It is strictly opt-in: when
// ControlRuntimeRoot is empty it does nothing and the daemon keeps
// drain-before-restart. A NewService failure never blocks startup: the daemon
// continues without a control record.
//
// The genuine reader behind instanceConfirmedStopped is the capture below, taken
// from a real runtimeproc.InspectRecord read of the prior control's record BEFORE
// this daemon acquires its own (which would overwrite it). Only a clean stopped
// record of a different instance on the same scope, with every operation
// completed and a valid identity, confirms the prior instance stopped; any
// missing/suspect/draining/incomplete/identical record keeps the fact false and
// the daemon on drain-before-restart. This keeps the hot path
// (executeAndDrain/handleTask/runTask/attemptWorkerRun) and the default
// drain-before-restart path byte-identical.
func (d *Daemon) startControlRuntime(ctx context.Context) {
	// Default-when-supported (capability-gated): when no ControlRuntimeRoot is
	// configured the control opts into owning a runtimeproc record only when a
	// native host executable is configured. The record root is derived from the
	// profile directory (filepath.Dir(NativeVscreenPreferencesPath), the same base
	// the application-service record uses) plus a "control-service" subdirectory —
	// a proper directory, NOT the executable path. This is the default-when-supported
	// analog of the worker gate and it is strictly fail-closed: a missing native
	// host, a non-absolute/missing preferences path, a missing account, or any
	// acquisition failure keeps drain-before-restart, and the retained-restart
	// decision still admits only when all three held facts hold (with the re-adopt
	// falling back to close, never orphaning). So a capability-gated control record
	// never changes the default drain path; it only lets a current, reconciling
	// server confirm a clean stopped prior instance and retain an execution.
	if strings.TrimSpace(d.cfg.ControlRuntimeRoot) == "" {
		if d.cfg.NativeHostExecutable == "" {
			return
		}
		root := filepath.Join(filepath.Dir(d.cfg.NativeVscreenPreferencesPath), "control-service")
		if !filepath.IsAbs(root) {
			return
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(root))
		if err != nil {
			return
		}
		d.cfg.ControlRuntimeRoot = filepath.Join(parent, filepath.Base(root))
	}
	root := strings.TrimSpace(d.cfg.ControlRuntimeRoot)
	if root == "" {
		return
	}
	if strings.TrimSpace(d.accountID) == "" {
		d.logger.Debug("control runtime: no account; keeping drain-before-restart")
		return
	}
	scope := runtimeproc.Scope{
		Backend:  d.cfg.ServerBaseURL,
		Account:  d.accountID,
		Profile:  d.cfg.Profile,
		DaemonID: d.cfg.DaemonID,
		Service:  "control",
	}
	identity, err := runtimeproc.NewIdentity(scope, d.cfg.NativeHostBuild)
	if err != nil {
		d.logger.Warn("control runtime: invalid identity; keeping drain-before-restart", "error", err)
		return
	}
	// Genuine, fail-closed reader of the prior control's record, taken before the
	// daemon acquires its own (which would overwrite it). Never a no-op: it acts on
	// the bytes of a real record via runtimeproc.InspectRecord.
	if rec, err := runtimeproc.InspectRecord(root, scope); err == nil && controlConfirmedStoppedRecord(rec, identity.InstanceID, scope) {
		d.priorControlConfirmedStopped.Store(true)
	}
	// Acquire the control record (best-effort; a failure keeps drain-before-restart
	// and does not block startup). A clean daemon exit publishes a "stopped" record
	// a successor control can confirm; the record is owned off the daemon ctx.
	controlCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		d.logger.Warn("control runtime: bootstrap failed; keeping drain-before-restart", "error", err)
		return
	}
	service, err := runtimeproc.NewService(runtimeproc.Config{Bootstrap: bootstrap})
	if err != nil {
		d.logger.Warn("control runtime: not acquired; keeping drain-before-restart", "error", err)
		return
	}
	d.controlService = service
	go func() {
		if err := service.Serve(controlCtx); err != nil {
			d.logger.Debug("control runtime service exited", "error", err)
		}
	}()
}
