package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestPagedReviewLargeApprovalMergeAndReceiptRecovery(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	worktreeTestGit(t, repo, "config", "user.name", "Review")
	worktreeTestGit(t, repo, "config", "user.email", "review@example.test")
	path := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	if err := os.WriteFile(filepath.Join(path, "large.txt"), []byte(strings.Repeat("reviewed large line\n", 500000)), 0600); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, path, "add", "large.txt")
	worktreeTestGit(t, path, "commit", "-m", "large feature")
	command := protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws1", TaskID: "task1", Path: path, Target: "main", Action: "manifest", ActorID: "owner"}
	result := d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	var view pagedReviewManifest
	if err := json.Unmarshal(result.Page, &view); err != nil {
		t.Fatal(err)
	}
	command.VersionID, command.SnapshotID = view.VersionID, view.VersionID
	command.Action, command.CommandID = "merge", "unapproved"
	if result := d.runRemoteReview(t.Context(), command); result.Error == "" {
		t.Fatal("merged without approval")
	}
	command.Action, command.CommandID = "approve", "approval"
	result = d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if err := json.Unmarshal(result.Page, &view); err != nil {
		t.Fatal(err)
	}
	if view.Review.State != "approved" {
		t.Fatal("approval not persisted")
	}
	command.Action, command.CommandID = "merge", "merge"
	result = d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if err := json.Unmarshal(result.Page, &view); err != nil {
		t.Fatal(err)
	}
	commit := view.Review.MergedCommit
	if commit == "" || view.Review.State != "merged" || worktreeTestGit(t, repo, "rev-parse", "HEAD") != commit {
		t.Fatal("merge not reflected in target")
	}
	key := localreview.RecordKey(localreview.Snapshot{Path: view.Header.Repository, Target: "main"})
	record, err := localreview.LoadRecord(root, key)
	if err != nil {
		t.Fatal(err)
	}
	record.State, record.MergedCommit = "approved", ""
	if err := localreview.SaveRecord(root, key, record); err != nil {
		t.Fatal(err)
	}
	result = d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if err := json.Unmarshal(result.Page, &view); err != nil {
		t.Fatal(err)
	}
	if view.Review.State != "merged" || view.Review.MergedCommit != commit || worktreeTestGit(t, repo, "rev-parse", "HEAD") != commit {
		t.Fatal("recovery did not preserve original merge")
	}
}

func TestPagedReviewRejectsStaleApprovalAndChangedReplay(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	worktreeTestGit(t, repo, "config", "user.name", "Review")
	worktreeTestGit(t, repo, "config", "user.email", "review@example.test")
	path := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	if err := os.WriteFile(filepath.Join(path, "new.txt"), []byte("reviewed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, path, "add", "new.txt")
	worktreeTestGit(t, path, "commit", "-m", "feature")
	command := protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws1", TaskID: "task1", Path: path, Target: "main", Action: "manifest", ActorID: "owner"}
	result := d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	var view pagedReviewManifest
	if err := json.Unmarshal(result.Page, &view); err != nil {
		t.Fatal(err)
	}
	command.VersionID, command.SnapshotID, command.Action, command.CommandID = view.VersionID, view.VersionID, "approve", "approve-once"
	for range 2 {
		if result := d.runRemoteReview(t.Context(), command); result.Error != "" {
			t.Fatal(result.Error)
		}
	}
	record, err := localreview.LoadRecord(root, localreview.RecordKey(localreview.Snapshot{Path: view.Header.Repository, Target: "main"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Events) != 1 {
		t.Fatal("approval replay added another event")
	}
	changed := command
	changed.ActorID = "another-user"
	if result := d.runRemoteReview(t.Context(), changed); result.Error == "" {
		t.Fatal("replay changed actor identity")
	}
	if err := os.WriteFile(filepath.Join(path, "new.txt"), []byte("not reviewed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target := worktreeTestGit(t, repo, "rev-parse", "HEAD")
	command.Action, command.CommandID = "merge", "stale-merge"
	if result := d.runRemoteReview(t.Context(), command); result.Error == "" {
		t.Fatal("stale approval merged new contents")
	}
	if worktreeTestGit(t, repo, "rev-parse", "HEAD") != target {
		t.Fatal("stale merge altered target")
	}
	command.Action, command.VersionID, command.SnapshotID, command.CommandID = "manifest", "", "", ""
	result = d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if err := json.Unmarshal(result.Page, &view); err != nil {
		t.Fatal(err)
	}
	if view.Review.State != "draft" {
		t.Fatal("new content retained old approval")
	}
}
