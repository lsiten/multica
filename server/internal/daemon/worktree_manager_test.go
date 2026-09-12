package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func worktreeTestDaemon(t *testing.T) *Daemon {
	return newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"status\":\"completed\"}"))
	}))
}

func TestWorktreeAutomaticCleanupPreservesFilesBesideRepository(t *testing.T) {
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", &execenv.GCMeta{AutoCleanup: true})
	repo := createWorktreeTestRepo(t)
	checkout := filepath.Join(root, "workdir", "repo")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "agent/output", checkout)
	if err := os.WriteFile(filepath.Join(root, "workdir", "report.txt"), []byte("deliverable"), 0o644); err != nil {
		t.Fatal(err)
	}
	if reason := d.cleanupManagedWorktree(context.Background(), worktreeCleanup{path: root, automatic: true}); reason != "output" {
		t.Fatalf("expected output protection, got %q", reason)
	}
}

func worktreeTestGit(t *testing.T, path string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", path}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestWorktreeAutomaticCleanupPreservesIgnoredDeliverables(t *testing.T) {
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", &execenv.GCMeta{AutoCleanup: true})
	repo := createWorktreeTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("report.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, repo, "add", ".gitignore")
	worktreeTestGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "ignore output")
	checkout := filepath.Join(root, "worktree")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "agent/output", checkout)
	if err := os.WriteFile(filepath.Join(checkout, "report.txt"), []byte("deliverable"), 0o644); err != nil {
		t.Fatal(err)
	}
	if reason := d.cleanupManagedWorktree(context.Background(), worktreeCleanup{path: root, automatic: true}); reason != "output" {
		t.Fatalf("expected ignored output protection, got %q", reason)
	}
}

func createWorktreeTestRepo(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	worktreeTestGit(t, path, "init", "-b", "main")
	worktreeTestGit(t, path, "config", "user.name", "Test")
	worktreeTestGit(t, path, "config", "user.email", "test@example.com")
	worktreeTestGit(t, path, "commit", "--allow-empty", "-m", "base")
	return path
}

func TestWorktreeManagerRejectsUnauthorizedRequests(t *testing.T) {
	d := worktreeTestDaemon(t)
	for _, tc := range []struct{ token, origin, profile string }{{"", "", ""}, {"wrong", "", ""}, {"test-token", "https://example.com", ""}, {"test-token", "", "other"}} {
		r := httptest.NewRequest(http.MethodDelete, "/worktrees", nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-Multica-Profile", tc.profile)
		w := httptest.NewRecorder()
		d.worktreeManagerHandler()(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthorized request accepted: %d", w.Code)
		}
	}
}

func TestWorktreeCleanupPreservesActiveAndUnownedDirectories(t *testing.T) {
	d := worktreeTestDaemon(t)
	path := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	d.markActiveEnvRoot(path)
	if reason := d.cleanupManagedWorktree(context.Background(), worktreeCleanup{path: path, discardChanges: true}); reason != "active" {
		t.Fatalf("active cleanup: %q", reason)
	}
	d.unmarkActiveEnvRoot(path)
	outside := t.TempDir()
	if reason := d.cleanupManagedWorktree(context.Background(), worktreeCleanup{path: outside, discardChanges: true}); reason != "unowned" {
		t.Fatalf("outside cleanup: %q", reason)
	}
	root, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	claim, _, err := execenv.LockEnvRootForReuse(root, "ws1/task1", path)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	if reason := d.cleanupManagedWorktree(context.Background(), worktreeCleanup{path: path}); reason != "active" {
		t.Fatalf("process lock ignored: %q", reason)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("active directory was removed", err)
	}
}

func TestWorktreeCleanupKeepsUncommittedWorkUnlessExplicitlyDiscarded(t *testing.T) {
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(root, "worktree")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "agent/test", path)
	if err := os.WriteFile(filepath.Join(path, "unfinished.txt"), []byte("valuable work"), 0o644); err != nil {
		t.Fatal(err)
	}
	if reason := d.cleanupManagedWorktree(context.Background(), worktreeCleanup{path: root}); reason != "dirty" {
		t.Fatalf("dirty cleanup: %q", reason)
	}
	if _, err := os.Stat(filepath.Join(path, "unfinished.txt")); err != nil {
		t.Fatal(err)
	}
	if reason := d.cleanupManagedWorktree(context.Background(), worktreeCleanup{path: root, discardChanges: true}); reason != "" {
		t.Fatalf("explicit cleanup: %q", reason)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("environment remains: %v", err)
	}
	if list := worktreeTestGit(t, repo, "worktree", "list", "--porcelain"); strings.Contains(list, path) {
		t.Fatal("stale worktree registration", list)
	}
	worktreeTestGit(t, repo, "show-ref", "--verify", "refs/heads/agent/test")
}

func TestWorktreeCleanupKeepsAllUnpushedCloneBranches(t *testing.T) {
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(root, "workdir")
	worktreeTestGit(t, repo, "clone", repo, path)
	worktreeTestGit(t, path, "checkout", "-b", "unpublished")
	worktreeTestGit(t, path, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "unpublished")
	worktreeTestGit(t, path, "checkout", "main")
	if reason := d.cleanupManagedWorktree(context.Background(), worktreeCleanup{path: root}); reason != "unpushed" {
		t.Fatalf("unpublished branch lost: %q", reason)
	}
}

func TestWorktreeAutomaticCleanupPreservesOutput(t *testing.T) {
	for _, output := range []bool{false, true} {
		d := worktreeTestDaemon(t)
		meta := &execenv.GCMeta{Kind: execenv.GCKindIssue, WorkspaceID: "ws1", TaskID: "task1", AutoCleanup: true, CompletedAt: time.Now()}
		root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", meta)
		if output {
			if err := os.Mkdir(filepath.Join(root, "output"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "output", "report.md"), []byte("report"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		d.autoCleanupCompletedWorktree(context.Background(), root)
		_, err := os.Stat(root)
		if output && err != nil {
			t.Fatal("output was removed", err)
		}
		if !output && !os.IsNotExist(err) {
			t.Fatal("safe completed environment was retained", err)
		}
	}
}

func TestWorktreeManagerBatchReturnsPerPathResults(t *testing.T) {
	d := worktreeTestDaemon(t)
	path := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	busy := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task2", nil)
	d.markActiveEnvRoot(busy)
	body, err := json.Marshal(managedWorktreeCleanupRequest{Paths: []string{path, busy, "/"}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodDelete, "/worktrees", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	d.worktreeManagerHandler()(w, r)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	var result managedWorktreeCleanupResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.RemovedPaths) != 1 || result.RemovedPaths[0] != path || result.Retained[busy] != "active" || result.Retained["/"] != "unowned" {
		t.Fatalf("bad cleanup result: %+v", result)
	}
}
