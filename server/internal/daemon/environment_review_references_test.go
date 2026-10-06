package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestPendingReviewProtectsSharedSourceAndItsReceiptRootUntilTaskCloses(t *testing.T) {
	category := "started"
	d := newGCTestDaemon(t, taskLifecycleTestHandler(t, func(string) protocol.TaskGCStatus {
		return protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: "completed", CompletedAt: time.Now(), LifecycleSupported: true, RetentionSupported: true, IssueID: "issue", IssueStatus: "in_review", IssueStatusCategory: category, CurrentAgent: false}
	}))
	d.cfg.EnvironmentRecycleEnabled = true
	source := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "source", nil)
	receiptRoot := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "review", nil)
	repo := createWorktreeTestRepo(t)
	checkout := filepath.Join(source, "worktree")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "agent/review-source", checkout)
	worktreeTestGit(t, checkout, "commit", "--allow-empty", "-m", "current delivery")
	if err := execenv.WriteReviewDirectory(receiptRoot, execenv.ReviewDirectory{WorkspaceID: "ws1", TaskID: "review", Path: checkout}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := localreview.Read(t.Context(), checkout, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := localreview.SaveRecord(receiptRoot, localreview.RecordKey(snapshot), localreview.Record{State: "open", SnapshotID: snapshot.ID, Snapshot: &snapshot, SourceHead: snapshot.Head}); err != nil {
		t.Fatal(err)
	}
	decision, decisionErr := localreview.InspectDecision(t.Context(), receiptRoot, checkout)
	if decisionErr != nil || decision.ReviewState != "open" {
		t.Fatalf("review fixture unavailable: %+v %v", decision, decisionErr)
	}
	foundRepos, inspectReason := inspectWorktreeRepositories(t.Context(), source, d.cfg.WorkspacesRoot)
	t.Logf("review decision=%+v repos=%v reason=%s bindingroot=%s", decision, foundRepos, inspectReason, managedCodeRoot(d.cfg.WorkspacesRoot, checkout))
	ctx := d.withEnvironmentReviewReferences(t.Context(), []string{source, receiptRoot})
	t.Logf("references=%v", ctx.Value(environmentReviewReferencesKey{}))
	for _, path := range []string{source, receiptRoot} {
		preview := d.cleanupUnreferencedEnvironment(t.Context(), path, "")
		if preview.Reason != "task_review" {
			t.Fatalf("review reference at %s not protected: %+v", path, preview)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	category = "done"
	for _, path := range []string{receiptRoot, source} {
		preview := d.cleanupUnreferencedEnvironment(t.Context(), path, "")
		if preview.Reason != "" {
			t.Fatalf("closed task pins historical review at %s: %+v", path, preview)
		}
		result := d.cleanupUnreferencedEnvironment(t.Context(), path, preview.Revision)
		if !result.Reclaimed {
			t.Fatalf("closed review not deleted: %+v", result)
		}
	}
}

func TestSnapshotCaptureProtectsSharedSourceWithoutKeepingAnIdleChat(t *testing.T) {
	d := newGCTestDaemon(t, taskLifecycleTestHandler(t, func(string) protocol.TaskGCStatus {
		return protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: "completed", CompletedAt: time.Now(), LifecycleSupported: true, RetentionSupported: true, ChatSessionID: "chat", ChatStatus: "active"}
	}))
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "source", nil)
	receiptRoot := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "reader", nil)
	workdir := filepath.Join(root, "workdir")
	writeLifecycleFile(t, filepath.Join(workdir, "code"), "capture safely")
	if err := execenv.WriteReviewDirectory(receiptRoot, execenv.ReviewDirectory{WorkspaceID: "ws1", TaskID: "reader", Path: workdir}); err != nil {
		t.Fatal(err)
	}
	store, err := localreview.OpenBlobStore(receiptRoot, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	finish, err := store.BeginRead(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	preview := d.cleanupUnreferencedEnvironment(t.Context(), root, "")
	if preview.Reason != "task_review" {
		t.Fatalf("live capture source not protected: %+v", preview)
	}
	finish()
	preview = d.cleanupUnreferencedEnvironment(t.Context(), root, "")
	if preview.Reason != "" {
		t.Fatalf("idle chat permanently pins code after capture: %+v", preview)
	}
}
