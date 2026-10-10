//go:build darwin || linux

package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// This file is the runtime/native acceptance for the F3/G retained-restart
// decision gate (control_restart_wiring.go + control_restart_admission.go). It
// proves that the third gate fact, instanceConfirmedStopped, is a GENUINE reader
// of a real runtimeproc control-record (not a hard-coded false / no-op) and that
// the gate ADMITS only on a confirmed-stopped prior control, refusing on a live
// (not-stopped) instance, a missing record, or a suspect/unknown record. The
// prior control's record is written by a REAL child process running
// RunControlService (re-executed via runtimeproc.Start, exactly as the other
// multi-process runtime tests), so the "stopped" the gate acts on is the bytes of
// a real record produced by a real process exiting cleanly — never a policy
// deadline, an offline runtime, a failed status, or a fabricated flag.

// controlChildScope is the scope both the real child control and the parent
// daemon use, so the parent's InspectRecord read points at the child's record.
// The child and the parent mint DIFFERENT instance ids (NewIdentity is random),
// which is exactly the "different instance, same scope" the confirmed-stopped
// check requires.
var controlChildScope = runtimeproc.Scope{
	Backend: "http://127.0.0.1:1", Account: "owned-account", DaemonID: "owned-daemon",
	Service: "control", Profile: "owned-fixture",
}

// spawnControlChild launches a real control child (RunControlService) pinned by
// canonical path + SHA256, returns the process and the scope lock dir, and
// reaps the child on cleanup. It is a real physical process, not an in-process
// fake: the child must be a distinct PID holding its own owner.lock.
func spawnControlChild(t *testing.T, root string) *runtimeproc.Process {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	identity, err := runtimeproc.NewIdentity(controlChildScope, "fixture/commit")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	process, err := runtimeproc.Start(ctx, runtimeproc.LaunchConfig{
		Executable:     executable,
		SHA256:         hex.EncodeToString(hash.Sum(nil)),
		Environment:    runtimeChildEnv(),
		Bootstrap:      bootstrap,
		StartupTimeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = process.Close()
		// A reaped child may still hold a freshly synced receipt under full-suite
		// load: writeRecord does an atomic temp+rename+dir-sync into the child's
		// persistent record directory, and the TempDir RemoveAll that follows this
		// cleanup can race that settle. Drain the record directory here (bounded) so
		// the suite is not flaky. The evidence is read during the test body, so
		// draining it after the body is safe.
		drainRecordDir(root)
	})
	// Confirm the child became ready as a distinct physical process.
	status, err := process.Client.Health(ctx)
	if err != nil {
		t.Fatalf("control child health failed: %v", err)
	}
	if status.State != "ready" && status.State != "starting" {
		t.Fatalf("control child state = %q, want ready/starting", status.State)
	}
	return process
}

// drainRecordDir removes a child's persistent runtime record directory once the
// child has been reaped. It retries a bounded number of times to absorb the
// load-dependent window in which the child's atomic writeRecord (temp file +
// rename + directory sync) has not fully settled, so the surrounding TempDir
// cleanup is not racy. It never blocks unbounded and never fails the test.
func drainRecordDir(root string) {
	for i := 0; i < 50; i++ {
		if err := os.RemoveAll(root); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// newControlParent builds a parent daemon that opts into a control runtime root
// (the same root the child wrote to) with the same scope, and exercises the
// opt-in control-runtime wiring.
func newControlParent(t *testing.T, root string) *Daemon {
	t.Helper()
	d := &Daemon{
		cfg: Config{
			ControlRuntimeRoot: root,
			ServerBaseURL:      controlChildScope.Backend,
			Profile:            controlChildScope.Profile,
			DaemonID:           controlChildScope.DaemonID,
			NativeHostBuild:    "fixture/commit",
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	d.accountID = controlChildScope.Account
	return d
}

// TestRetainedRestartAdmissionRuntimeConfirmsStoppedChild is the positive runtime
// proof: a real control child that has cleanly stopped produces a "stopped"
// record, and the parent's instanceConfirmedStopped reads it genuinely (true), so
// with the other two held facts real the gate ADMITS. This is the "all three
// facts real -> admit" guarantee backed by a real multi-process record.
func TestRetainedRestartAdmissionRuntimeConfirmsStoppedChild(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "control-service")

	// A real child control acquires the record and is then cleanly stopped, which
	// publishes a "stopped" record with a completed operation and releases its lock.
	child := spawnControlChild(t, root)
	if err := child.Stop(t.Context()); err != nil {
		t.Fatalf("control child did not stop cleanly: %v", err)
	}

	d := newControlParent(t, root)
	// startControlRuntime captures the prior confirmed-stopped fact from a real
	// InspectRecord read of the child's "stopped" record, then (best-effort) takes
	// over ownership so the capture reads the prior record, not the current one.
	d.startControlRuntime(t.Context())

	if !d.instanceConfirmedStopped() {
		t.Fatal("instanceConfirmedStopped must be true: a real child control stopped cleanly")
	}
	// The other two facts are the live-held sources: a reconciling server and a
	// retained (active) task. With all three real the gate must admit.
	d.executionReconcileSupported.Store(true)
	d.activeTasks.Store(1)
	a := d.retainedRestartAdmission("gateway")
	if !a.Admitted {
		t.Fatalf("gate refused although all three facts are real: reason=%q", a.ReasonCode)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("admitted decision must validate: %v", err)
	}
}

// TestRetainedRestartAdmissionRuntimeRefusesLiveChild is the fail-closed runtime
// proof: while the prior control child is still a live owner (record "ready", not
// "stopped"), instanceConfirmedStopped reads false, so a capability-replacing
// restart is refused on instance_suspect. The gate never treats a live (not
// stopped) instance as stopped.
func TestRetainedRestartAdmissionRuntimeRefusesLiveChild(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "control-service")

	// The child stays alive (a live owner, not stopped). The parent's capture reads
	// the child's "ready" record and fails closed; the parent cannot take over
	// (the child holds the lock), so the daemon keeps drain-before-restart.
	spawnControlChild(t, root)

	d := newControlParent(t, root)
	d.startControlRuntime(t.Context())

	if d.instanceConfirmedStopped() {
		t.Fatal("instanceConfirmedStopped must fail closed while the prior control is a live (not stopped) owner")
	}
	d.executionReconcileSupported.Store(true)
	d.activeTasks.Store(1)
	a := d.retainedRestartAdmission("gateway")
	if a.Admitted {
		t.Fatal("a capability-replacing restart was admitted while the prior control is still live")
	}
	if a.ReasonCode != RetainedRestartReasonSuspect {
		t.Fatalf("reason = %q, want %q (a live instance is suspect, never confirmed stopped)", a.ReasonCode, RetainedRestartReasonSuspect)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("refused decision must validate: %v", err)
	}
}

// TestRetainedRestartAdmissionRuntimeRefusesNoRoot is the opt-in fail-closed
// proof: with no control runtime root configured there is no prior control record
// to confirm, so instanceConfirmedStopped stays false and the gate refuses on
// instance_suspect — the default (non-retained) drain-before-restart path is
// preserved when the control record is not opted in.
func TestRetainedRestartAdmissionRuntimeRefusesNoRoot(t *testing.T) {
	d := &Daemon{
		cfg:    Config{},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	d.accountID = controlChildScope.Account
	d.startControlRuntime(t.Context()) // no root -> does nothing

	if d.instanceConfirmedStopped() {
		t.Fatal("instanceConfirmedStopped must fail closed when no control runtime root is configured")
	}
	d.executionReconcileSupported.Store(true)
	d.activeTasks.Store(1)
	a := d.retainedRestartAdmission("gateway")
	if a.Admitted {
		t.Fatal("a capability-replacing restart was admitted with no confirmed-stopped record")
	}
	if a.ReasonCode != RetainedRestartReasonSuspect {
		t.Fatalf("reason = %q, want %q", a.ReasonCode, RetainedRestartReasonSuspect)
	}
}
