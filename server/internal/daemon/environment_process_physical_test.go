package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestEnvironmentPhysicalOwnerCommitsOnlyAfterWorkerRelease(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	owner, err := newEnvironmentPhysicalOwner(root, t.TempDir(), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.close()
	repo := createWorktreeTestRepo(t)
	result, err := owner.prepare(ctx, execenv.PhysicalPrepareParams{WorkspacesRoot: "/must-not-use", WorkspaceID: "workspace", TaskID: "physical-task", AgentName: "fake", LocalWorktree: &execenv.LocalWorktreeParams{LocalPath: repo, RetainCheckout: true}})
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(owner.root, result.Reservation.RootDir)
	if err != nil || !filepath.IsLocal(rel) {
		t.Fatalf("caller chose physical root: %+v", result)
	}
	claim, err := execenv.ClaimPhysicalRoot(root, result.Reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	if err = owner.confirmRoot(result.Reservation); err != nil {
		t.Fatal(err)
	}
	worker := &execenv.LocalWorktree{Path: result.WorktreePath, WorkDir: result.WorkDir, Branch: result.Branch}
	if err = worker.BeginSharedExecution(ctx, execenv.SharedWorktreeDelivery{TaskID: "physical-task", Namespace: owner.journal}); err != nil {
		t.Fatal(err)
	}
	participant, err := worker.PhysicalParticipant()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(result.WorkDir, "worker.txt"), []byte("owned worker change"), 0600); err != nil {
		t.Fatal(err)
	}
	permit, err := owner.beginFinish(ctx, result.Reservation.ID, participant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.confirmFinish(result.Reservation.ID, permit); err == nil {
		t.Fatal("service committed before worker released kernel participant")
	}
	if err = worker.ReleasePhysicalParticipant(permit); err != nil {
		t.Fatal(err)
	}
	outcome, err := owner.confirmFinish(result.Reservation.ID, permit)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Commit == "" {
		t.Fatalf("missing delivery commit: %+v", outcome)
	}
	before := worktreeTestGit(t, repo, "rev-parse", result.Branch)
	repeated, err := owner.confirmFinish(result.Reservation.ID, permit)
	if err != nil || repeated.Commit != outcome.Commit {
		t.Fatalf("idempotent outcome changed: %+v %v", repeated, err)
	}
	if after := worktreeTestGit(t, repo, "rev-parse", result.Branch); after != before {
		t.Fatal("repeated confirm created another commit")
	}
	receipts, err := execenv.PendingSharedWorktreeDeliveries(ctx, owner.journal)
	if err != nil || len(receipts) != 1 || receipts[0].Commit != outcome.Commit {
		t.Fatalf("delivery receipt missing: %+v %v", receipts, err)
	}
	if err = execenv.AcknowledgeSharedWorktreeDelivery(ctx, receipts[0]); err != nil {
		t.Fatal(err)
	}
}

func TestPhysicalOwnerFencesLateCompletionGeneration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	owner, err := newEnvironmentPhysicalOwner(root, t.TempDir(), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.close()
	repo := createWorktreeTestRepo(t)
	result, err := owner.prepare(ctx, execenv.PhysicalPrepareParams{WorkspacesRoot: "/must-not-use", WorkspaceID: "workspace", TaskID: "physical-task", AgentName: "fake", LocalWorktree: &execenv.LocalWorktreeParams{LocalPath: repo, RetainCheckout: true}})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := execenv.ClaimPhysicalRoot(root, result.Reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	if err = owner.confirmRoot(result.Reservation); err != nil {
		t.Fatal(err)
	}
	// The current generation still matches this task's reservation, so a
	// completion is authorized.
	if err = owner.verifyCompletionGeneration(result.Reservation.ID); err != nil {
		t.Fatalf("current generation rejected its own completion: %v", err)
	}
	// A later task re-claims the same root by advancing the current marker to a
	// different reservation on the same path/inode.
	later := result.Reservation
	later.ID = "later-generation"
	current, err := json.Marshal(later)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(result.Reservation.RootDir, ".physical-current.json"), current, 0600); err != nil {
		t.Fatal(err)
	}
	if err = owner.verifyCompletionGeneration(result.Reservation.ID); err == nil {
		t.Fatal("late completion was not fenced after the root advanced to a new generation")
	}
	if err = owner.verifyCompletionGeneration("unknown-generation"); err == nil {
		t.Fatal("unknown preparation was treated as an authorized completion")
	}
}

func TestPhysicalOwnerSettleReleasesMaintenanceBarrierOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	owner, err := newEnvironmentPhysicalOwner(root, t.TempDir(), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.close()
	settles := 0
	// The service binds this to release the maintenance barrier held while a
	// worker's Git user is active; it must fire exactly once per preparation.
	owner.onSettle = func() { settles++ }
	repo := createWorktreeTestRepo(t)
	result, err := owner.prepare(ctx, execenv.PhysicalPrepareParams{WorkspacesRoot: "/must-not-use", WorkspaceID: "workspace", TaskID: "physical-task", AgentName: "fake", LocalWorktree: &execenv.LocalWorktreeParams{LocalPath: repo, RetainCheckout: true}})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := execenv.ClaimPhysicalRoot(root, result.Reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	if err = owner.confirmRoot(result.Reservation); err != nil {
		t.Fatal(err)
	}
	worker := &execenv.LocalWorktree{Path: result.WorktreePath, WorkDir: result.WorkDir, Branch: result.Branch}
	if err = worker.BeginSharedExecution(ctx, execenv.SharedWorktreeDelivery{TaskID: "physical-task", Namespace: owner.journal}); err != nil {
		t.Fatal(err)
	}
	participant, err := worker.PhysicalParticipant()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(result.WorkDir, "worker.txt"), []byte("owned worker change"), 0600); err != nil {
		t.Fatal(err)
	}
	permit, err := owner.beginFinish(ctx, result.Reservation.ID, participant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.confirmFinish(result.Reservation.ID, permit); err == nil {
		t.Fatal("service committed before worker released kernel participant")
	}
	if err = worker.ReleasePhysicalParticipant(permit); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.confirmFinish(result.Reservation.ID, permit); err != nil {
		t.Fatal(err)
	}
	if settles != 1 {
		t.Fatalf("maintenance barrier released %d times, want exactly 1", settles)
	}
	// A repeated confirm must not release the barrier again.
	if _, err = owner.confirmFinish(result.Reservation.ID, permit); err != nil {
		t.Fatal(err)
	}
	if settles != 1 {
		t.Fatalf("repeated confirm released the barrier %d times, want 1", settles)
	}
}

func TestPhysicalFingerprintReusesExistingScopeWithoutRawResources(t *testing.T) {
	task := Task{WorkspaceID: "workspace", RuntimeID: "runtime", AgentID: "agent", IssueID: "issue", ProjectID: "project", Repos: []RepoData{{URL: "https://owned.invalid/repository", Ref: "main"}}}
	scope, err := managedScopeForTask(task)
	if err != nil {
		t.Fatal(err)
	}
	scope.ManagedBy = execenv.ManagedEnvProvenanceManagedBy
	narrow := Task{WorkspaceID: task.WorkspaceID, RuntimeID: task.RuntimeID, AgentID: task.AgentID, IssueID: task.IssueID, ProjectID: task.ProjectID, physicalRepositoryScope: scope.RepositoryScope}
	if !managedReuseScopeMatches(task, &scope) || !managedReuseScopeMatches(narrow, &scope) {
		t.Fatal("narrow physical scope changed same-workline reuse")
	}
	encoded, err := json.Marshal(narrow)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(scope.RepositoryScope)) {
		t.Fatal("internal scope fingerprint became exported Task JSON")
	}
	narrow.physicalRepositoryScope = "invalid"
	if _, err = managedScopeForTask(narrow); err == nil {
		t.Fatal("malformed physical fingerprint accepted")
	}
}

// TestPhysicalOwnerFencesSelectAttachGeneration completes the result-revision
// contract on the select/attach consumption path. The completion path is fenced
// by TestPhysicalOwnerFencesLateCompletionGeneration; this proves the same
// stale result is rejected by verifyPhysicalPreparationIdentity, which is the
// gate attachPhysical and confirmFinish run before touching Git state.
func TestPhysicalOwnerFencesSelectAttachGeneration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	owner, err := newEnvironmentPhysicalOwner(root, t.TempDir(), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.close()
	repo := createWorktreeTestRepo(t)
	result, err := owner.prepare(ctx, execenv.PhysicalPrepareParams{WorkspacesRoot: "/must-not-use", WorkspaceID: "workspace", TaskID: "physical-task", AgentName: "fake", LocalWorktree: &execenv.LocalWorktreeParams{LocalPath: repo, RetainCheckout: true}})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := execenv.ClaimPhysicalRoot(root, result.Reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	if err = owner.confirmRoot(result.Reservation); err != nil {
		t.Fatal(err)
	}
	owner.mu.Lock()
	preparation := owner.preparations[result.Reservation.ID]
	owner.mu.Unlock()
	if preparation == nil {
		t.Fatalf("no preparation recorded for %s", result.Reservation.ID)
	}
	// A freshly prepared root is current: the generation marker and the
	// checkout inode both match, so the select/attach path authorizes it.
	if err = verifyPhysicalPreparationIdentity(preparation); err != nil {
		t.Fatalf("current select/attach identity fenced: %v", err)
	}
	// A later task re-claims the same root by advancing the current marker to a
	// different reservation on the same path/inode. The select/attach path must
	// fence this, not only the completion path.
	later := result.Reservation
	later.ID = "later-generation"
	current, err := json.Marshal(later)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(result.Reservation.RootDir, ".physical-current.json"), current, 0600); err != nil {
		t.Fatal(err)
	}
	if err = verifyPhysicalPreparationIdentity(preparation); err == nil {
		t.Fatal("select/attach accepted a stale generation after the root advanced")
	}
	// The same stale generation must also fence the completion path, so both
	// consumption paths agree on the result-revision contract.
	if err = owner.verifyCompletionGeneration(result.Reservation.ID); err == nil {
		t.Fatal("completion accepted a stale generation after the root advanced")
	}
}

// TestEnvironmentProcessServiceAttachFencesStaleGeneration exercises the
// daemon-level attachPhysical gate (the target named in the F1 handoff for the
// source-selection race/replay work). It proves that once a later task re-claims
// the same root by advancing the current marker, attachPhysical refuses the
// stale preparation at verifyPhysicalPreparationIdentity instead of committing
// Git state over a root that moved to a new generation.
func TestEnvironmentProcessServiceAttachFencesStaleGeneration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	owner, err := newEnvironmentPhysicalOwner(root, t.TempDir(), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.close()
	repo := createWorktreeTestRepo(t)
	result, err := owner.prepare(ctx, execenv.PhysicalPrepareParams{WorkspacesRoot: "/must-not-use", WorkspaceID: "workspace", TaskID: "physical-task", AgentName: "fake", LocalWorktree: &execenv.LocalWorktreeParams{LocalPath: repo, RetainCheckout: true}})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := execenv.ClaimPhysicalRoot(root, result.Reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	if err = owner.confirmRoot(result.Reservation); err != nil {
		t.Fatal(err)
	}
	worker := &execenv.LocalWorktree{Path: result.WorktreePath, WorkDir: result.WorkDir, Branch: result.Branch}
	if err = worker.BeginSharedExecution(ctx, execenv.SharedWorktreeDelivery{TaskID: "physical-task", Namespace: owner.journal}); err != nil {
		t.Fatal(err)
	}
	participant, err := worker.PhysicalParticipant()
	if err != nil {
		t.Fatal(err)
	}
	// selectPhysical normally sets Selected; the fence is what attachPhysical
	// must enforce after that, so mark the selection current directly.
	owner.mu.Lock()
	preparation := owner.preparations[result.Reservation.ID]
	if preparation == nil {
		t.Fatalf("no preparation recorded for %s", result.Reservation.ID)
	}
	preparation.Selected = true
	owner.mu.Unlock()
	svc := &environmentProcessService{physical: owner, daemon: &Daemon{}}
	// A later task re-claims the same root by advancing the current marker to a
	// different reservation on the same path/inode.
	later := result.Reservation
	later.ID = "later-generation"
	current, err := json.Marshal(later)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(result.Reservation.RootDir, ".physical-current.json"), current, 0600); err != nil {
		t.Fatal(err)
	}
	if err = svc.attachPhysical(ctx, result.Reservation.ID, participant); err == nil {
		t.Fatal("attachPhysical authorized a stale generation after the root advanced")
	} else if !strings.Contains(err.Error(), "generation") && !strings.Contains(err.Error(), "identity") {
		t.Fatalf("attachPhysical fenced for the wrong reason: %v", err)
	}
}

// TestPhysicalOwnerRecoversJournalWithoutFabricatingIdentity proves the item 7
// crash-recovery contract: a restarted environment service must recover a
// durable in-flight preparation as a protected, fail-closed record rather than
// forgetting it, and it must not fabricate a kernel identity for it.
func TestPhysicalOwnerRecoversJournalWithoutFabricatingIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	root := t.TempDir()
	journal := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	owner, err := newEnvironmentPhysicalOwner(root, journal, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.close()
	repo := createWorktreeTestRepo(t)
	result, err := owner.prepare(ctx, execenv.PhysicalPrepareParams{WorkspacesRoot: "/must-not-use", WorkspaceID: "workspace", TaskID: "physical-task", AgentName: "fake", LocalWorktree: &execenv.LocalWorktreeParams{LocalPath: repo, RetainCheckout: true}})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := execenv.ClaimPhysicalRoot(root, result.Reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	if err = owner.confirmRoot(result.Reservation); err != nil {
		t.Fatal(err)
	}
	// The durable journal now holds the preparation under its reservation id.
	if _, err = os.Stat(filepath.Join(journal, result.Reservation.ID+".json")); err != nil {
		t.Fatalf("durable preparation not written: %v", err)
	}
	// Simulate a crash: a second owner on the same journal must recover the
	// preparation as a protected record instead of forgetting the root.
	restarted, err := newEnvironmentPhysicalOwner(root, journal, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.close()
	restarted.mu.Lock()
	recovered := restarted.preparations[result.Reservation.ID]
	restarted.mu.Unlock()
	if recovered == nil {
		t.Fatal("recovery forgot an in-flight preparation")
	}
	if recovered.Result.Reservation != result.Reservation {
		t.Fatalf("recovery changed the reservation identity: %+v", recovered.Result.Reservation)
	}
	// The recovered preparation cannot be re-verified (its kernel identity is
	// nil), so it fail-closes instead of fabricating authority to settle.
	if err = restarted.verifyCompletionGeneration(result.Reservation.ID); err == nil {
		t.Fatal("recovered preparation was treated as a verified completion")
	}
}
