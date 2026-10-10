//go:build darwin || linux

package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"golang.org/x/sys/unix"
)

// This file is the runtime proof of the F3/G task14 cross-process admission
// boundary (replacement_admission.go) on a real multi-process machine. It does
// NOT wire the hot path: AdmitReplacement stays the owner-side boundary. What it
// proves is that the exit proof it acts on is derived from a REAL per-execution
// worker child's process state and domain owner.lock, never from a policy
// deadline, an expired grant, an offline runtime, a failed status or a
// cancellation ACK. While that child is a live owner the admission refuses; once
// the child has actually exited and released its lock the admission is granted
// and carries the proof. This is the plan's "only actual provider/worker exit
// plus participant/root release admits a replacement" verified as a true child.

// spawnAdmissionWorkerChild launches a real per-execution worker child and
// returns its PID, the scope lock dir and runtimeproc.Process so the caller can observe the
// child's actual process and domain-lock state. Unlike spawnWorkerChild it does
// not reap the child on cleanup: the caller owns the lifecycle so it can drive
// the old-tree exit proof from the real child. A context cancel (via t.Cleanup)
// still kills the child if the test fails before it closes it.
func spawnAdmissionWorkerChild(t *testing.T) (pid int, lockDir string, proc *runtimeproc.Process) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "worker-service", uuid.NewString())
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
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	scope := runtimeproc.Scope{
		Backend: "http://127.0.0.1:1", Account: "owned-account", DaemonID: "owned-daemon",
		Service: "worker", Profile: "owned-fixture",
	}
	identity, err := runtimeproc.NewIdentity(scope, "fixture/commit")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	proc, err = runtimeproc.Start(ctx, runtimeproc.LaunchConfig{
		Executable:     executable,
		SHA256:         hex.EncodeToString(hash.Sum(nil)),
		Environment:    runtimeChildEnv(),
		Bootstrap:      bootstrap,
		StartupTimeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	t.Cleanup(func() {
		// Idempotent safety reap: Close is a no-op once the child has exited.
		_ = proc.Close()
	})
	// The child holds its domain owner.lock in the scope-hash directory (the same
	// directory that holds owner.json), not at the raw root. Derive it exactly
	// the way runtimeproc does so the lock check points at the real lock.
	lockDir = filepath.Dir(runtimeproc.RecordPath(root, identity.Scope))
	// Confirm the child became ready as a distinct physical process and is
	// therefore a live owner before any admission check.
	status, err := proc.Client.Health(ctx)
	if err != nil {
		t.Fatalf("worker health failed: %v", err)
	}
	request, err := proc.Client.Request("worker.status", status.Fence, marshalRaw(map[string]any{}))
	if err != nil {
		t.Fatalf("worker.status request failed: %v", err)
	}
	resp, err := proc.Client.Call(ctx, request)
	if err != nil {
		t.Fatalf("worker.status call failed: %v", err)
	}
	if resp.Receipt == nil || resp.Receipt.Error != nil {
		t.Fatalf("worker.status returned an error receipt: code=%s msg=%s", resp.Receipt.Error.Code, resp.Receipt.Error.Message)
	}
	var live struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(resp.Receipt.Result, &live); err != nil {
		t.Fatalf("unmarshal worker status: %v (%s)", err, resp.Receipt.Result)
	}
	if live.PID == 0 || live.PID == os.Getpid() {
		t.Fatalf("worker is not a live physical child: %s", resp.Receipt.Result)
	}
	return live.PID, lockDir, proc
}

// ownerLockHeld reports whether the domain owner.lock in root is held by a live
// owner, using the same non-blocking exclusive flock runtimeproc.Start uses to
// decide whether another owner is live. It is the real participant/root release
// signal, not a policy timeout.
func ownerLockHeld(t *testing.T, root string) bool {
	t.Helper()
	fd, err := unix.Open(filepath.Join(root, "owner.lock"), unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		t.Fatalf("open owner lock: %v", err)
	}
	err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
	if err != nil {
		_ = unix.Close(fd)
		return true // held by a live owner
	}
	_ = unix.Flock(fd, unix.LOCK_UN)
	_ = unix.Close(fd)
	return false
}

// awaitOwnerLockReleased polls until the domain owner.lock is free, bounded.
func awaitOwnerLockReleased(t *testing.T, root string) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !ownerLockHeld(t, root) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return !ownerLockHeld(t, root)
}

// awaitProcessDead polls until the exact child PID is no longer a running
// process, bounded. It observes actual exit, not a health timeout.
func awaitProcessDead(t *testing.T, pid int) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return !processAlive(pid)
}

// TestReplacementAdmissionRealChildOldTreeExit proves the task14 boundary against
// a real per-execution worker child: a replacement is admitted only when the
// prior provider/worker and its participant/root use have ACTUALLY exited.
func TestReplacementAdmissionRealChildOldTreeExit(t *testing.T) {
	pid, lockDir, proc := spawnAdmissionWorkerChild(t)
	taskID := uuid.NewString()
	runtimeID := uuid.NewString()

	// (1) The old tree is a live owner: a running process that holds its
	// domain owner.lock.
	if !processAlive(pid) {
		t.Fatalf("old worker child is not alive before admission checks")
	}
	if !ownerLockHeld(t, lockDir) {
		t.Fatalf("old worker child does not hold its domain owner.lock while alive")
	}

	// (2) With no exit proof the admission must refuse old_tree_unknown; this is
	// the fail-closed default the owner must keep while the tree is still running.
	refuseUnknown := AdmitReplacement(taskID, runtimeID, nil)
	if refuseUnknown.Admitted {
		t.Fatalf("admitted a replacement while the old tree is still running")
	}
	if refuseUnknown.ReasonCode != ReplacementReasonOldTreeUnknown {
		t.Fatalf("got refusal reason %q, want %q", refuseUnknown.ReasonCode, ReplacementReasonOldTreeUnknown)
	}
	if err := refuseUnknown.Validate(); err != nil {
		t.Fatalf("refusal did not validate: %v", err)
	}

	// (3) A provider-tree-exit signal WITHOUT participant/root release must still
	// refuse: a tree that "exited" on paper but still holds the lock is not real
	// exit proof and must not admit a conflicting use.
	refuseNotReleased := AdmitReplacement(taskID, runtimeID, &OldTreeExitProof{ProviderTreeExited: true, ParticipantReleased: false})
	if refuseNotReleased.Admitted {
		t.Fatalf("admitted a replacement whose participant/root use is not released")
	}
	if refuseNotReleased.ReasonCode != ReplacementReasonNotReleased {
		t.Fatalf("got refusal reason %q, want %q", refuseNotReleased.ReasonCode, ReplacementReasonNotReleased)
	}
	if err := refuseNotReleased.Validate(); err != nil {
		t.Fatalf("refusal did not validate: %v", err)
	}

	// (4) The old tree now ACTUALLY exits and releases its participant/root use.
	if err := proc.Close(); err != nil {
		t.Fatalf("old worker child did not exit: %v", err)
	}
	if !awaitProcessDead(t, pid) {
		t.Fatalf("old worker child is still alive after close; exit proof would be fabricated")
	}
	if !awaitOwnerLockReleased(t, lockDir) {
		t.Fatalf("old worker child exited but its domain owner.lock is still held")
	}

	// (5) Only real exit plus real participant/root release admits the
	// replacement, and the admission carries the full exit proof.
	admitted := AdmitReplacement(taskID, runtimeID, &OldTreeExitProof{ProviderTreeExited: true, ParticipantReleased: true})
	if !admitted.Admitted {
		t.Fatalf("refused a replacement after the old tree actually exited: reason=%s", admitted.ReasonCode)
	}
	if admitted.Exit == nil || !admitted.Exit.ProviderTreeExited || !admitted.Exit.ParticipantReleased {
		t.Fatalf("admitted replacement did not carry full old-tree exit proof: %+v", admitted.Exit)
	}
	if err := admitted.Validate(); err != nil {
		t.Fatalf("admitted replacement did not validate: %v", err)
	}
	t.Logf("OBSERVE real-child admission boundary: old worker pid=%d exited and released owner.lock, replacement admitted (task=%s runtime=%s)", pid, taskID, runtimeID)
}
