package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
)

func TestRetainedArtifactCleanupPreservesSourceAndNestedRepositories(t *testing.T) {
	for _, kind := range []string{"tracked", "untracked", "outside", "output", "nested", "ignored"} {
		t.Run(kind, func(t *testing.T) {
			d := worktreeTestDaemon(t)
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", &execenv.GCMeta{AutoCleanup: true, CompletedAt: time.Now().Add(-48 * time.Hour)})
			repo := createWorktreeTestRepo(t)
			checkout := filepath.Join(root, "workdir", "repo")
			worktreeTestGit(t, repo, "worktree", "add", "-b", "agent/artifacts", checkout)
			if kind != "untracked" {
				writeLifecycleFile(t, filepath.Join(checkout, ".gitignore"), "node_modules/\n")
				worktreeTestGit(t, checkout, "add", ".gitignore")
				worktreeTestGit(t, checkout, "commit", "-m", "ignore generated artifacts")
			}
			path := filepath.Join(checkout, "node_modules", "report.txt")
			switch kind {
			case "outside":
				path = filepath.Join(root, "workdir", "loose", "node_modules", "report.txt")
			case "output":
				path = filepath.Join(root, "output", "node_modules", "report.txt")
			case "nested":
				path = filepath.Join(checkout, "node_modules", "nested")
				worktreeTestGit(t, repo, "clone", repo, path)
			}
			if kind != "nested" {
				writeLifecycleFile(t, path, "unique source")
			}
			if kind == "tracked" {
				worktreeTestGit(t, checkout, "add", "-f", "node_modules/report.txt")
				worktreeTestGit(t, checkout, "commit", "-m", "tracked source under familiar directory")
				writeLifecycleFile(t, path, "new uncommitted source")
			}
			writeLifecycleFile(t, filepath.Join(root, "output", "keep.txt"), "")

			d.gcWorkspace(t.Context(), filepath.Dir(root), &gcStats{byPattern: map[string]int{}})

			_, err := os.Stat(path)
			if kind == "ignored" {
				if !os.IsNotExist(err) {
					t.Fatalf("regenerable ignored artifact retained: %v", err)
				}
			} else if err != nil {
				t.Fatalf("%s deliverable removed: %v", kind, err)
			}
			if _, err := os.Stat(filepath.Join(root, "output", "keep.txt")); err != nil {
				t.Fatalf("zero-byte output lost: %v", err)
			}
		})
	}
}

func TestLegacyGCRespectsReuseLockAndReviewLease(t *testing.T) {
	for _, protection := range []string{"reuse", "review"} {
		for _, action := range []gcAction{gcActionClean, gcActionCleanArtifacts, gcActionCleanManagedArtifacts} {
			t.Run(protection+string(rune('0'+action)), func(t *testing.T) {
				d := worktreeTestDaemon(t)
				root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", &execenv.GCMeta{CompletedAt: time.Now().Add(-48 * time.Hour)})
				sentinel := filepath.Join(root, execenv.ManagedReclaimableArtifactSubpaths()[0], "keep")
				writeLifecycleFile(t, sentinel, "cache")
				if protection == "reuse" {
					workspace, err := os.OpenRoot(d.cfg.WorkspacesRoot)
					if err != nil {
						t.Fatal(err)
					}
					defer workspace.Close()
					claim, _, err := execenv.LockEnvRootForReuse(workspace, "ws1/task1", root)
					if err != nil {
						t.Fatal(err)
					}
					defer claim.Release()
				} else {
					store, err := localreview.OpenBlobStore(root, 1<<20)
					if err != nil {
						t.Fatal(err)
					}
					defer store.Close()
					release, err := store.BeginRead(t.Context(), time.Now())
					if err != nil {
						t.Fatal(err)
					}
					defer release()
				}

				d.applyGCAction(root, action, &gcStats{byPattern: map[string]int{}})

				if _, err := os.Stat(sentinel); err != nil {
					t.Fatalf("%s protection ignored: %v", protection, err)
				}
			})
		}
	}
}

func writeLifecycleFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
