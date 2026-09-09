package daemon

import (
	"github.com/multica-ai/multica/server/pkg/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteReviewBranchesWithoutSnapshot(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	if err := os.WriteFile(filepath.Join(path, "oversized.txt"), []byte(strings.Repeat("x", 9<<20)), 0600); err != nil {
		t.Fatal(err)
	}
	result := d.runRemoteReview(t.Context(), protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws1", TaskID: "task1", Path: path, Target: "missing", Action: "branches"})
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if len(result.Snapshot) != 0 {
		t.Fatal("branch listing built a snapshot")
	}
	if strings.Join(result.Branches, ",") != "feature,main" {
		t.Fatalf("unexpected branches: %v", result.Branches)
	}
}

func TestRemoteReviewBranchesRejectsOtherTaskDirectory(t *testing.T) {
	d := worktreeTestDaemon(t)
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	result := d.runRemoteReview(t.Context(), protocol.LocalReviewCommand{RuntimeID: "runtime", WorkspaceID: "ws1", TaskID: "other-task", Path: path, Action: "branches"})
	if result.Error == "" {
		t.Fatal("branch listing bypassed task ownership")
	}
}
