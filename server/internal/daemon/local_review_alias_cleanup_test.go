package daemon

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestLocalReviewAliasCleanupPreservesReusedRepository(t *testing.T) {
	d := worktreeTestDaemon(t)
	priorRoot := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "prior1", nil)
	currentRoot := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "current1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(priorRoot, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "feature", path)
	if err := execenv.WriteReviewDirectory(currentRoot, execenv.ReviewDirectory{WorkspaceID: "ws1", TaskID: "current1", Path: path}); err != nil {
		t.Fatal(err)
	}
	if reason := d.cleanupManagedWorktree(context.Background(), worktreeCleanup{path: currentRoot}); reason != "" {
		t.Fatal(reason)
	}
	if worktreeTestGit(t, path, "rev-parse", "--is-inside-work-tree") != "true" {
		t.Fatal("alias cleanup removed the shared repository")
	}
	request := worktreeReviewRequest{WorkspaceID: "ws1", TaskID: "current1", Path: path, Target: "main"}
	_, recordRoot, err := d.resolveReviewRoot(context.Background(), request)
	if err != nil || recordRoot != execenv.ReviewArchivePath(d.cfg.WorkspacesRoot, "ws1", "current1") {
		t.Fatal("alias cleanup lost the archived MR binding", recordRoot, err)
	}
}
