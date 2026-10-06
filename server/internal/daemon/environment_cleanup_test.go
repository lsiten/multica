package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestUnusedEnvironmentIsDeletedWithoutCreatingAnyArchive(t *testing.T) {
	d := newGCTestDaemon(t, taskLifecycleTestHandler(t, func(string) protocol.TaskGCStatus {
		return protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: "completed", CompletedAt: time.Now(), LifecycleSupported: true, RetentionSupported: true}
	}))
	d.cfg.EnvironmentRecycleEnabled = true
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", &execenv.GCMeta{WorkspaceID: "ws1", TaskID: "task", RuntimeID: "runtime", AgentID: "agent", Kind: execenv.GCKindIssue, CompletedAt: time.Now()})
	writeLifecycleFile(t, filepath.Join(root, "output", "old-output.txt"), "remove with unused task")
	writeLifecycleFile(t, filepath.Join(root, "workdir", "old-file.txt"), "remove unused code")
	ctx := withEnvironmentScope(t.Context(), environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "runtime", Automatic: true})
	preview := d.cleanupUnreferencedEnvironment(ctx, root, "")
	if preview.Reason != "" || preview.Revision == "" {
		t.Fatalf("unused environment not eligible: %+v", preview)
	}
	result := d.cleanupUnreferencedEnvironment(ctx, root, preview.Revision)
	if !result.Reclaimed || result.Reason != "" {
		t.Fatalf("direct cleanup failed: %+v", result)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("unused directory remains: %v", err)
	}
	for _, directory := range []string{".environment-archive", ".local-mr-archive"} {
		if _, err := os.Stat(filepath.Join(d.cfg.WorkspacesRoot, directory)); !os.IsNotExist(err) {
			t.Fatalf("direct cleanup left a persistent archive %s: %v", directory, err)
		}
	}
}

func TestDirectCleanupUnregistersOnlyItsOwnLinkedWorktree(t *testing.T) {
	d := newGCTestDaemon(t, taskLifecycleTestHandler(t, func(string) protocol.TaskGCStatus {
		return protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: "failed", CompletedAt: time.Now(), LifecycleSupported: true, RetentionSupported: true}
	}))
	d.cfg.EnvironmentRecycleEnabled = true
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", nil)
	repo := createWorktreeTestRepo(t)
	checkout := filepath.Join(root, "worktree")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "agent/unused", checkout)
	other := filepath.Join(t.TempDir(), "another")
	worktreeTestGit(t, repo, "worktree", "add", "-b", "user/keep", other)
	writeLifecycleFile(t, filepath.Join(checkout, "dirty.txt"), "delete unused dirty checkout")
	ctx := withEnvironmentScope(t.Context(), environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "runtime", Automatic: true})
	preview := d.cleanupUnreferencedEnvironment(ctx, root, "")
	result := d.cleanupUnreferencedEnvironment(ctx, root, preview.Revision)
	if !result.Reclaimed {
		t.Fatalf("unused linked checkout not deleted: %+v", result)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("other user worktree removed: %v", err)
	}
	registered := worktreeTestGit(t, repo, "worktree", "list", "--porcelain")
	if strings.Contains(registered, checkout) || !strings.Contains(registered, other) {
		t.Fatalf("wrong worktree registrations removed: %s", registered)
	}
}

func TestDirectCleanupPreservesRepositoryUsedByAnotherWorktree(t *testing.T) {
	d := newGCTestDaemon(t, taskLifecycleTestHandler(t, func(string) protocol.TaskGCStatus {
		return protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: "completed", CompletedAt: time.Now(), LifecycleSupported: true, RetentionSupported: true}
	}))
	d.cfg.EnvironmentRecycleEnabled = true
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", nil)
	repo := createWorktreeTestRepo(t)
	checkout := filepath.Join(root, "workdir")
	worktreeTestGit(t, repo, "clone", repo, checkout)
	other := filepath.Join(t.TempDir(), "dependent")
	worktreeTestGit(t, checkout, "worktree", "add", "-b", "user/dependent", other)
	ctx := withEnvironmentScope(t.Context(), environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "runtime", Automatic: true})
	preview := d.cleanupUnreferencedEnvironment(ctx, root, "")
	result := d.cleanupUnreferencedEnvironment(ctx, root, preview.Revision)
	if result.Reclaimed {
		t.Fatal("shared repository was deleted")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("source repository removed: %v", err)
	}
	if head := worktreeTestGit(t, other, "rev-parse", "HEAD"); head == "" {
		t.Fatal("dependent worktree lost git objects")
	}
}
