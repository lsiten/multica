package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// prepareRuntimeRoot returns a canonical, private, prepared runtime root.
func prepareRuntimeRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = runtimeproc.PrepareRoot(root); err != nil {
		t.Fatal(err)
	}
	return root
}

// writeRuntimeRecord publishes a private owner record the same way the runtime
// process writes one, using only exported helpers, so the daemon test can stage
// a prior owner without spawning a real child.
func writeRuntimeRecord(t *testing.T, root string, identity runtimeproc.Identity, address, state string, receipts ...runtimeproc.Receipt) {
	t.Helper()
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	record := runtimeproc.Record{
		Identity:    identity,
		Token:       bootstrap.Token,
		Address:     address,
		State:       state,
		Fence:       bootstrap.Fence,
		ReplayEpoch: 1,
		Operations:  map[string]runtimeproc.Receipt{},
	}
	for _, receipt := range receipts {
		record.Operations[receipt.RequestID] = receipt
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := runtimeproc.RecordPath(root, identity.Scope)
	dir := filepath.Dir(recordPath)
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(dir, ".receipt-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if err = runtimeproc.ProtectPrivateArtifact(f); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err = f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = runtimeproc.ReplacePrivateArtifact(f.Name(), recordPath); err != nil {
		t.Fatal(err)
	}
	if err = runtimeproc.SyncPrivateArtifactDirectory(dir); err != nil {
		t.Fatal(err)
	}
}

func crashDetectDaemon() *Daemon {
	return &Daemon{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// A prior owner that is non-stopped but unreachable must be retired, so a
// crashed service's record does not permanently block the split process.
func TestCrashDetectAndReconcileRetiresUnreachablePriorOwner(t *testing.T) {
	root := prepareRuntimeRoot(t)
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{
		Backend: "http://test.invalid", Account: "fixture", Profile: "test", DaemonID: "daemon", Service: "probe",
	}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	// Port 1 is a valid loopback origin with no listener: the prior owner is dead.
	writeRuntimeRecord(t, root, identity, "http://127.0.0.1:1", "ready")

	if err = crashDetectDaemon().crashDetectAndReconcile(context.Background(), root, identity.Scope, "probe"); err != nil {
		t.Fatalf("dead prior owner should be retired: %v", err)
	}
	after, err := runtimeproc.ReadRecord(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "stopped" {
		t.Fatalf("retired owner must be stopped, got %q", after.State)
	}
	// The retire must preserve the prior owner's receipts in a private archive.
	if _, err = runtimeproc.ReadReconciledRecord(root, identity); err != nil {
		t.Fatalf("retired history must be archived: %v", err)
	}
}

// A reachable prior owner must never be retired; the pre-check fails closed and
// the record stays intact with no archive written.
func TestCrashDetectAndReconcileNeverRetiresLivePriorOwner(t *testing.T) {
	root := prepareRuntimeRoot(t)
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{
		Backend: "http://test.invalid", Account: "fixture", Profile: "test", DaemonID: "daemon", Service: "probe",
	}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	// A loopback server that answers the handshake proves the prior owner is live.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(runtimeproc.Response{Status: runtimeproc.Status{
			ReplayEpoch: 1,
			Identity:    identity,
			State:       "ready",
			Fence:       runtimeproc.Fence{SupervisorEpoch: 1, ResourceEpoch: 1, Revision: 1},
		}})
	}))
	defer server.Close()
	writeRuntimeRecord(t, root, identity, server.URL, "ready")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = crashDetectDaemon().crashDetectAndReconcile(ctx, root, identity.Scope, "probe"); err == nil {
		t.Fatal("live prior owner must not be reconciled")
	}
	after, err := runtimeproc.ReadRecord(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "ready" {
		t.Fatalf("live owner must not be retired, got %q", after.State)
	}
	if _, err := runtimeproc.ReadReconciledRecord(root, identity); !os.IsNotExist(err) {
		t.Fatalf("a live owner must leave no reconciliation archive: %v", err)
	}
}

// A clean stopped record and a missing record are both no-ops; the crash path
// only retires a non-stopped owner, and only when one actually exists.
func TestCrashDetectAndReconcileSkipsStoppedAndMissingRecord(t *testing.T) {
	scope := runtimeproc.Scope{
		Backend: "http://test.invalid", Account: "fixture", Profile: "test", DaemonID: "daemon", Service: "probe",
	}
	d := crashDetectDaemon()

	// Missing record: nothing to reconcile.
	root := prepareRuntimeRoot(t)
	if err := d.crashDetectAndReconcile(context.Background(), root, scope, "probe"); err != nil {
		t.Fatalf("missing record must be a no-op: %v", err)
	}

	// Stopped record: already replaceable, nothing to reconcile.
	stopped, err := runtimeproc.NewIdentity(scope, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	writeRuntimeRecord(t, root, stopped, "http://127.0.0.1:1", "stopped")
	if err = d.crashDetectAndReconcile(context.Background(), root, scope, "probe"); err != nil {
		t.Fatalf("stopped record must be a no-op: %v", err)
	}
	after, err := runtimeproc.ReadRecord(root, stopped)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "stopped" {
		t.Fatalf("stopped record must be untouched, got %q", after.State)
	}
}

func TestCrashDetectAndReconcileRetiresStoppedOwnerWithUncertainReceipts(t *testing.T) {
	// Given a stopped owner whose final operation never received a completion receipt.
	root := prepareRuntimeRoot(t)
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: "http://test.invalid", Account: "fixture", Profile: "test", DaemonID: "daemon", Service: "probe"}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	receipt := runtimeproc.Receipt{RequestID: "1:uncertain", State: "pending", Digest: "fixture"}
	writeRuntimeRecord(t, root, identity, "http://127.0.0.1:1", "stopped", receipt, runtimeproc.Receipt{RequestID: "1:complete", State: "completed", Result: json.RawMessage(`{"kept":true}`)})
	// When crash recovery examines the exited owner.
	if err := crashDetectDaemon().crashDetectAndReconcile(t.Context(), root, identity.Scope, "probe"); err != nil {
		t.Fatal(err)
	}
	// Then uncertain receipts are archived, never replayed or silently discarded.
	archive, err := runtimeproc.ReadReconciledRecord(root, identity)
	if err != nil {
		t.Fatalf("uncertain stopped owner was skipped: %v", err)
	}
	if len(archive.Operations) != 2 || string(archive.Operations["1:complete"].Result) != `{"kept":true}` || archive.Operations[receipt.RequestID].State != "pending" {
		t.Fatalf("receipt lost: %+v", archive.Operations)
	}
	current, err := runtimeproc.ReadRecord(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != "stopped" || len(current.Operations) != 0 {
		t.Fatalf("old owner still blocks replacement: %+v", current.Operations)
	}
}

func TestCrashDetectAndReconcileProtectsLockedStoppedOwner(t *testing.T) {
	root := prepareRuntimeRoot(t)
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: "http://test.invalid", Account: "fixture", Profile: "test", DaemonID: "daemon", Service: "probe"}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	writeRuntimeRecord(t, root, identity, "http://127.0.0.1:1", "stopped", runtimeproc.Receipt{RequestID: "uncertain", State: "pending"})
	lock, err := lockMirrorProcessDomain(filepath.Join(filepath.Dir(runtimeproc.RecordPath(root, identity.Scope)), "owner.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := crashDetectDaemon().crashDetectAndReconcile(t.Context(), root, identity.Scope, "probe"); err == nil {
		t.Fatal("locked owner was retired")
	}
	record, err := runtimeproc.ReadRecord(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	if record.Operations["uncertain"].State != "pending" {
		t.Fatal("locked owner's receipts changed")
	}
	if _, err := runtimeproc.ReadReconciledRecord(root, identity); !os.IsNotExist(err) {
		t.Fatalf("locked owner archived: %v", err)
	}
}
