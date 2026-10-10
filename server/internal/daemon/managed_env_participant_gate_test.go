package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

// newManagedReuseParticipantGateDaemon seeds a non-local managed env root that a
// prior task would reuse and returns the daemon, the code root the reuse gates
// on (the managed code root the exclusive FD and the shared-directory
// participant both pin), and the task. The nil localAssignment is what drives the
// non-local branch of lockReusablePriorEnvRoot, so the participant gate is
// exercised in isolation from the local-worktree path.
func newManagedReuseParticipantGateDaemon(t *testing.T) (*Daemon, string, Task) {
	t.Helper()
	root := t.TempDir()
	envRoot := filepath.Join(root, "ws-leader", "0123456789ab")
	workDir := filepath.Join(envRoot, "workdir")
	writeLeaderTaskMarker(t, workDir, "agent-leader", "issue-leader")
	writeLeaderManagedEnvProvenance(t, workDir, "ws-leader", "issue-leader", "agent-leader")

	task := leaderReuseTestTask("task-managed-participant")
	task.PriorWorkDir = workDir

	d := &Daemon{logger: discardLogger()}
	d.cfg.WorkspacesRoot = root
	d.envRootBusyWait = 10 * time.Second
	return d, envRoot, task
}

// TestLockManagedEnvRootHoldsThenClaimsForLiveSharedParticipant proves the core
// of the fix: a non-local managed reuse that finds a live shared-directory
// participant on the code root waits it out (HOLD) rather than claiming over it
// just because the exclusive FD is free, then claims once the participant exits.
func TestLockManagedEnvRootHoldsThenClaimsForLiveSharedParticipant(t *testing.T) {
	d, envRoot, task := newManagedReuseParticipantGateDaemon(t)

	lease, err := execenv.ReserveSharedDirectoryPreparation(context.Background(), envRoot)
	if err != nil {
		t.Fatalf("reserve live participant: %v", err)
	}
	t.Cleanup(func() { lease.Finish(context.Background(), nil) })

	// Release the participant a moment after the reuse begins so the gate sees a
	// live participant, waits, and then claims once it clears.
	go func() {
		time.Sleep(300 * time.Millisecond)
		lease.Finish(context.Background(), nil)
	}()

	claim, workDir, _, ok, err := d.lockReusablePriorEnvRoot(context.Background(), task, nil, "")
	if err != nil {
		t.Fatalf("reuse over a live participant: %v", err)
	}
	if !ok || claim == nil {
		t.Fatalf("reuse declined while it should have waited out the participant and claimed")
	}
	if workDir == "" {
		t.Fatalf("reuse claimed no workdir")
	}
	claim.Release()
}

// TestLockManagedEnvRootReturnsCauseWhenCancelledWhileParticipantLive proves that
// a cancellation while a live participant holds the code root surfaces the cause
// (so the caller ends the task) instead of declining to a fresh Prepare.
func TestLockManagedEnvRootReturnsCauseWhenCancelledWhileParticipantLive(t *testing.T) {
	d, envRoot, task := newManagedReuseParticipantGateDaemon(t)

	lease, err := execenv.ReserveSharedDirectoryPreparation(context.Background(), envRoot)
	if err != nil {
		t.Fatalf("reserve live participant: %v", err)
	}
	defer lease.Finish(context.Background(), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	claim, _, _, ok, err := d.lockReusablePriorEnvRoot(ctx, task, nil, "")
	if err == nil {
		t.Fatalf("expected a cancellation to surface as the cause, not a silent claim or decline")
	}
	if ok || claim != nil {
		t.Fatalf("reuse claimed while a live participant held the code root")
	}
}

// TestLockManagedEnvRootDeclinesFreshPrepareWhenParticipantWedged proves that a
// live participant the busy window cannot clear is declined to a fresh
// environment (the safe fallback for a genuinely wedged root) and released rather
// than held forever.
func TestLockManagedEnvRootDeclinesFreshPrepareWhenParticipantWedged(t *testing.T) {
	d, envRoot, task := newManagedReuseParticipantGateDaemon(t)
	d.envRootBusyWait = 300 * time.Millisecond

	lease, err := execenv.ReserveSharedDirectoryPreparation(context.Background(), envRoot)
	if err != nil {
		t.Fatalf("reserve live participant: %v", err)
	}
	defer lease.Finish(context.Background(), nil)

	claim, _, _, ok, err := d.lockReusablePriorEnvRoot(context.Background(), task, nil, "")
	if err != nil {
		t.Fatalf("reuse over a wedged participant: %v", err)
	}
	if ok || claim != nil {
		t.Fatalf("reuse claimed over a wedged live participant")
	}
}
