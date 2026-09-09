package daemon

import (
	"github.com/multica-ai/multica/server/internal/daemon/localreview"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorktreeCleanupArchivesBeforeRemovingGitCheckout(t *testing.T) {
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(root, "worktree")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "agent/test", path)
	if err := os.WriteFile(filepath.Join(path, "unfinished.txt"), []byte("valuable work"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := localreview.OpenBlobStore(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	version, err := store.CaptureWorking(t.Context(), localreview.WorkingVersionRequest{Path: path, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TouchVersion(t.Context(), id, time.Now().Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	key := localreview.RecordKey(version.RecoverySnapshot(id))
	if err := localreview.SaveRecord(root, key, localreview.Record{SnapshotID: id, VersionID: id, State: "approved"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, ".local-review-cache", "blobs", version.Files[0].New.ID)); err != nil {
		t.Fatal(err)
	}
	if reason := d.cleanupManagedWorktree(t.Context(), worktreeCleanup{path: root, discardChanges: true}); reason != "unavailable" {
		t.Fatalf("cleanup accepted incomplete archive: %q", reason)
	}
	if _, err := os.Stat(filepath.Join(path, "unfinished.txt")); err != nil {
		t.Fatal("checkout was deleted before archival completed", err)
	}
}

func TestWorktreeCleanupWaitsForInitialReviewCapture(t *testing.T) {
	d := worktreeTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
	repo := createWorktreeTestRepo(t)
	path := filepath.Join(root, "worktree")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "agent/test", path)
	store, err := localreview.OpenBlobStore(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	release, err := store.BeginRead(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if reason := d.cleanupManagedWorktree(t.Context(), worktreeCleanup{path: root, discardChanges: true}); reason != "review" {
		t.Fatalf("active capture was not protected: %q", reason)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("active checkout was removed", err)
	}
	release()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if reason := d.cleanupManagedWorktree(t.Context(), worktreeCleanup{path: root, discardChanges: true}); reason != "" {
		t.Fatalf("released capture blocked cleanup: %q", reason)
	}
}
