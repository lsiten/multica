package daemon

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestGCRetainedWorktreeReclaimsOnlyExpiredArtifacts(t *testing.T) {
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", &execenv.GCMeta{AutoCleanup: true, CompletedAt: time.Now().Add(-48 * time.Hour)})
	repo := createWorktreeTestRepo(t)
	worktreeTestGit(t, repo, "worktree", "add", "-b", "agent/artifacts", filepath.Join(root, "workdir"))
	if err := os.WriteFile(filepath.Join(root, "workdir", ".gitignore"), []byte("node_modules/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"output/report.txt", "workdir/node_modules/cache"} {
		path := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("keep deliverable"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	d.gcWorkspace(t.Context(), filepath.Dir(root), &gcStats{byPattern: map[string]int{}})

	if _, err := os.Stat(filepath.Join(root, "output/report.txt")); err != nil {
		t.Fatalf("deliverable lost: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "workdir/node_modules")); !os.IsNotExist(err) {
		t.Fatalf("expired artifact retained: %v", err)
	}
}

func TestGCCleanTaskDirPreservesUnpublishedClone(t *testing.T) {
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	checkout := filepath.Join(root, "workdir")
	worktreeTestGit(t, repo, "clone", repo, checkout)
	worktreeTestGit(t, checkout, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "unpublished")

	_, removed := d.cleanTaskDir(root)

	if removed {
		t.Fatal("terminal issue cleanup discarded the only unpublished commit")
	}
	if _, err := os.Stat(checkout); err != nil {
		t.Fatal(err)
	}
}

func TestGCPrunePreservesUniqueAgentBranchesAndTheirCache(t *testing.T) {
	d := newGCTestDaemon(t, http.NewServeMux())
	d.cfg.GCRepoTTL = time.Hour
	bare := newEvictTestRepo(t, d, "ws1", testRepoURL)
	worktreeTestGit(t, bare, "fetch", "origin", "+refs/heads/*:refs/remotes/origin/*")
	tree := worktreeTestGit(t, bare, "rev-parse", "HEAD^{tree}")
	head := worktreeTestGit(t, bare, "rev-parse", "HEAD")
	commit := worktreeTestGit(t, bare, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit-tree", tree, "-p", head, "-m", "unique delivery")
	for _, branch := range []string{"agent/delivery/one", "agent/delivery/two"} {
		worktreeTestGit(t, bare, "branch", branch, commit)
	}
	writeLastUsed(t, bare, time.Now().Add(-48*time.Hour))

	d.pruneRepoWorktreesContext(context.Background(), d.cfg.WorkspacesRoot, &gcStats{byPattern: map[string]int{}})

	if _, err := os.Stat(bare); err != nil {
		t.Fatalf("pending delivery cache removed: %v", err)
	}
	for _, branch := range []string{"agent/delivery/one", "agent/delivery/two"} {
		if !gitRefExists(t, bare, "refs/heads/"+branch) {
			t.Fatalf("unpublished branch removed: %s", branch)
		}
	}
}
