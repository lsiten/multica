package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRemoteReviewCommandReadsAndMergesOnOwningRuntime(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	worktreeTestGit(t, repo, "config", "user.name", "Review")
	worktreeTestGit(t, repo, "config", "user.email", "review@example.test")
	path := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	if err := os.WriteFile(filepath.Join(path, "file.txt"), []byte("new content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, path, "add", "file.txt")
	worktreeTestGit(t, path, "commit", "-m", "feature")
	command := protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws1", TaskID: "task1", Path: path, Target: "main", Action: "read", ClaimToken: "claim"}
	result := d.runRemoteReview(context.Background(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	var snapshot localreview.Snapshot
	if err := json.Unmarshal(result.Snapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Files) != 1 || snapshot.Files[0].Path != "file.txt" {
		t.Fatal("missing file diff")
	}
	command.Action, command.SnapshotID = "merge", snapshot.ID
	command.ID = "unapproved-merge"
	if rejected := d.runRemoteReview(context.Background(), command); rejected.Error == "" {
		t.Fatal("remote merge bypassed runtime-owned approval")
	}
	command.Action = "approve"
	command.ID = "initial-approve"
	if approved := d.runRemoteReview(context.Background(), command); approved.Error != "" {
		t.Fatal(approved.Error)
	}
	command.Action = "merge"
	command.ID = "initial-merge"
	command.ActorID, command.Comment = "runtime-owner", "reviewed merge"
	result = d.runRemoteReview(context.Background(), command)
	if result.Error != "" || result.MergedCommit == "" {
		t.Fatal("merge failed", result.Error)
	}
	if worktreeTestGit(t, repo, "rev-parse", "HEAD") != result.MergedCommit {
		t.Fatal("owning checkout not updated")
	}
	if worktreeTestGit(t, path, "rev-parse", "HEAD") != snapshot.Head {
		t.Fatal("source was changed")
	}
	mergeCommit := result.MergedCommit
	// Simulate loss of the final receipt after Git updated the target.
	key := localreview.RecordKey(snapshot)
	record, err := localreview.LoadRecord(root, key)
	if err != nil {
		t.Fatal(err)
	}
	record.State, record.MergedCommit = "approved", ""
	if err := localreview.SaveRecord(root, key, record); err != nil {
		t.Fatal(err)
	}
	// Re-delivery after a lost acknowledgement must return the prior result.
	changedPayload := command
	changedPayload.Comment = "changed after the merge"
	if result := d.runRemoteReview(context.Background(), changedPayload); result.Error == "" {
		t.Fatal("merge recovery accepted reused operation ID with changed comment")
	}
	changedActor := command
	changedActor.ActorID = "another-user"
	if result := d.runRemoteReview(context.Background(), changedActor); result.Error == "" {
		t.Fatal("merge recovery accepted reused operation ID from another actor")
	}
	result = d.runRemoteReview(context.Background(), command)
	if result.Error != "" || result.MergedCommit != mergeCommit {
		t.Fatal("merge result did not recover", result.Error)
	}
	var recoveredReceipt localreview.Record
	if err := json.Unmarshal(result.Review, &recoveredReceipt); err != nil || recoveredReceipt.State != "merged" {
		t.Fatal("recovered response omitted the runtime-owned review", err)
	}
	if len(recoveredReceipt.Events) == 0 {
		t.Fatal("recovery event missing")
	}
	recoveryEvent := recoveredReceipt.Events[len(recoveredReceipt.Events)-1]
	if recoveryEvent.Kind != "merge_recovered" || recoveryEvent.ActorID != "runtime-owner" || recoveryEvent.Comment != "reviewed merge" {
		t.Fatal("recovery lost original actor or comment", recoveryEvent)
	}
	if worktreeTestGit(t, repo, "rev-parse", "HEAD") != mergeCommit {
		t.Fatal("re-delivery executed another merge")
	}
	recovered, err := localreview.LoadRecord(root, key)
	if err != nil || recovered.State != "merged" || recovered.MergedCommit != mergeCommit {
		t.Fatal("owning runtime did not recover its authoritative receipt", recovered.State, err)
	}
	// A new source revision is a new review round, not a recovery of the old merge.
	worktreeTestGit(t, path, "commit", "--allow-empty", "-m", "next review round")
	command.Action = "read"
	result = d.runRemoteReview(context.Background(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if err := json.Unmarshal(result.Snapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	command.Action, command.SnapshotID, command.ID = "approve", snapshot.ID, "next-approval"
	if result := d.runRemoteReview(context.Background(), command); result.Error != "" {
		t.Fatal(result.Error)
	}
	command.Action, command.ID = "merge", "next-merge"
	result = d.runRemoteReview(context.Background(), command)
	if result.Error != "" || result.MergedCommit == "" || result.MergedCommit == mergeCommit || worktreeTestGit(t, repo, "rev-parse", "HEAD") != result.MergedCommit {
		t.Fatal("new review reused an old merge receipt", result.Error)
	}
	command.RuntimeID = "other-runtime"
	if result := d.runRemoteReview(context.Background(), command); result.Error == "" {
		t.Fatal("accepted wrong runtime")
	}
}

func TestRemoteMergeCanRetryFixedPreconditionWithNewCommand(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	worktreeTestGit(t, repo, "config", "user.name", "Review")
	worktreeTestGit(t, repo, "config", "user.email", "review@example.test")
	path := filepath.Join(root, "worktree")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	worktreeTestGit(t, path, "commit", "--allow-empty", "-m", "feature")
	command := protocol.LocalReviewCommand{ID: "read", RuntimeID: "runtime", WorkspaceID: "ws1", TaskID: "task1", Path: path, Target: "main", Action: "read"}
	result := d.runRemoteReview(context.Background(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	var snapshot localreview.Snapshot
	if err := json.Unmarshal(result.Snapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	privateFile := filepath.Join(repo, "pending.txt")
	if err := os.WriteFile(privateFile, []byte("user edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	command.Action, command.SnapshotID, command.ID = "merge", snapshot.ID, "merge-first"
	approval := command
	approval.Action = "approve"
	approval.ID = "approval-before-merge"
	if result := d.runRemoteReview(context.Background(), approval); result.Error != "" {
		t.Fatal(result.Error)
	}
	if result := d.runRemoteReview(context.Background(), command); result.Error == "" {
		t.Fatal("merged dirty target")
	}
	if err := os.Remove(privateFile); err != nil {
		t.Fatal(err)
	}
	if result := d.runRemoteReview(context.Background(), command); result.Error == "" {
		t.Fatal("same command repeated a write")
	}
	command.ID = "merge-retry"
	result = d.runRemoteReview(context.Background(), command)
	if result.Error != "" || result.MergedCommit == "" {
		t.Fatal("new explicit retry failed", result.Error)
	}
}
