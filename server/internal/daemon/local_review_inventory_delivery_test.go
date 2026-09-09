package daemon

import (
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestLocalReviewInventoryOmitsFinalizedDeliveryShortcut(t *testing.T) {
	for _, finalized := range []bool{false, true} {
		t.Run(map[bool]string{false: "in-place", true: "delivered"}[finalized], func(t *testing.T) {
			// Given an external directory binding whose task root still exists.
			d := worktreeTestDaemon(t)
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
			repo := createWorktreeTestRepo(t)
			canonical, err := filepath.EvalSymlinks(repo)
			if err != nil {
				t.Fatal(err)
			}
			binding := execenv.ReviewDirectory{WorkspaceID: "ws1", TaskID: "task1", Path: canonical}
			if finalized {
				binding.SourcePath = filepath.Join(root, "removed-worktree")
				binding.Commit = worktreeTestGit(t, repo, "rev-parse", "HEAD")
				binding.Branch = "main"
			}
			if err := execenv.WriteReviewDirectory(root, binding); err != nil {
				t.Fatal(err)
			}
			// When the runtime lists available MR repository entry points.
			rows, err := d.managedWorktrees(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("inventory rows = %d, want retained task root", len(rows))
			}
			// Then a delivery destination is not offered as the removed worktree.
			if finalized && len(rows[0].Repositories) != 0 {
				t.Fatal("offered finalized delivery repository as a worktree")
			}
			if !finalized && (len(rows[0].Repositories) != 1 || rows[0].Repositories[0] != canonical) {
				t.Fatal("omitted live in-place binding")
			}
		})
	}
}
