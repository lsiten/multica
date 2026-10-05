package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRunTaskRetainsAndReusesLocalWorktreeWithSeparateRunRoots(t *testing.T) {
	d, _, cleanup := newLeaderReuseTestDaemon(t)
	defer cleanup()
	d.localPathLocks = NewLocalPathLocker()
	repo := createWorktreeTestRepo(t)
	d.cfg.DaemonID = "local-worktree-daemon"
	ref, err := json.Marshal(localDirectoryRef{LocalPath: repo, DaemonID: d.cfg.DaemonID, ExecutionMode: "worktree"})
	if err != nil {
		t.Fatal(err)
	}
	first := leaderReuseTestTask("task-first")
	first.IsLeaderTask = false
	first.ProjectID = "project"
	first.ProjectResources = []ProjectResourceData{{ID: "resource", ResourceType: "local_directory", ResourceRef: ref}}
	result, err := d.runTask(context.Background(), first, "claude", 0, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	if result.DurableWorkDir != "" {
		t.Fatal("retained checkout was replaced by user directory")
	}
	before, err := os.Stat(result.WorkDir)
	if err != nil {
		t.Fatalf("successful local checkout was deleted: %v", err)
	}
	second := first
	second.ID, second.PriorWorkDir, second.PriorSessionID = "task-second", result.WorkDir, result.SessionID
	next, err := d.runTask(context.Background(), second, "claude", 0, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(next.WorkDir)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("physical checkout was rebuilt: %q %q %v", result.WorkDir, next.WorkDir, err)
	}
	if sameDir(t, result.EnvRoot, next.EnvRoot) || next.CodeRoot == "" {
		t.Fatal("reused local code did not retain per-run configuration isolation")
	}
	if _, err := os.Stat(filepath.Join(repo, ".multica")); !os.IsNotExist(err) {
		t.Fatal("runtime sidecars leaked into user checkout")
	}
	assignment, err := localDirectoryAssignmentForTask(second, d.cfg.DaemonID)
	if err != nil {
		t.Fatal(err)
	}
	workdir, ok := shouldReusePriorWorkdir(second, assignment, d.cfg.WorkspacesRoot)
	if !ok {
		t.Fatal("retained checkout cannot be validated")
	}
	root := managedCodeRoot(d.cfg.WorkspacesRoot, workdir)
	parent, err := os.OpenRoot(d.cfg.WorkspacesRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	canonicalBase, err := filepath.EvalSymlinks(d.cfg.WorkspacesRoot)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(canonicalBase, root)
	if err != nil {
		t.Fatal(err)
	}
	claim, _, err := execenv.LockEnvRootForReuse(parent, relative, root)
	if err != nil || claim == nil {
		t.Fatalf("could not hold checkout lease: %v", err)
	}
	defer claim.Release()
	third := second
	third.ID = "task-third"
	forked, err := d.runTask(context.Background(), third, "claude", 0, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	if sameDir(t, forked.WorkDir, next.WorkDir) {
		t.Fatal("concurrent continuation shared the locked checkout")
	}
}

func TestRunTaskRestoresAndReattachesColdLocalCheckout(t *testing.T) {
	d, _, cleanup := newLeaderReuseTestDaemon(t)
	defer cleanup()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/gc-check") {
			json.NewEncoder(w).Encode(protocol.TaskGCStatus{Status: "completed", WorkspaceID: "ws-leader", RuntimeID: "rt-leader", AgentID: "agent-leader", LifecycleSupported: true})
			return
		}
		w.Write([]byte("{}"))
	}))
	defer server.Close()
	d.client = NewClient(server.URL)
	d.cfg.ServerBaseURL = server.URL
	d.cfg.DaemonID = "cold-local-daemon"
	d.localPathLocks = NewLocalPathLocker()
	repo := createWorktreeTestRepo(t)
	ref, err := json.Marshal(localDirectoryRef{LocalPath: repo, DaemonID: d.cfg.DaemonID, ExecutionMode: "worktree"})
	if err != nil {
		t.Fatal(err)
	}
	first := leaderReuseTestTask("task-first")
	first.IsLeaderTask = false
	first.ProjectResources = []ProjectResourceData{{ID: "resource", ResourceType: "local_directory", ResourceRef: ref}}
	result, err := d.runTask(t.Context(), first, "claude", 0, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	d.recordEnvironmentCompletion(first, result, d.logger)
	preview := d.archiveEnvironmentOperation(t.Context(), result.EnvRoot, "", "")
	archived := d.archiveEnvironmentOperation(t.Context(), result.EnvRoot, preview.Revision, strings.Repeat("a", 64))
	if !archived.Reclaimed {
		t.Fatalf("local archive failed: %+v", archived)
	}
	second := first
	second.ID, second.PriorWorkDir = "task-second", result.WorkDir
	next, err := d.runTask(t.Context(), second, "claude", 0, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	if !sameDir(t, next.WorkDir, result.WorkDir) || next.CodeRoot == "" {
		t.Fatalf("cold code directory not reused: %+v", next)
	}
	common := worktreeTestGit(t, next.WorkDir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	origin := worktreeTestGit(t, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if !sameDir(t, common, origin) {
		t.Fatal("cold checkout did not reattach to original repository")
	}
}

func TestReusedWorktreeSnapshotDoesNotWaitForDirectoryRelease(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
	}))
	d.localPathLocks = NewLocalPathLocker()
	d.cancelPollInterval = 5 * time.Millisecond
	path := t.TempDir()
	holder, err := d.localPathLocks.Acquire(t.Context(), path, "holder", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	release, err := d.acquireReusedWorktreeSnapshot(ctx, Task{ID: "waiting"}, &localDirectoryAssignment{AbsPath: path, RealPath: path}, d.logger)
	if err != nil || release == nil {
		t.Fatalf("snapshot waited for occupied directory: %v", err)
	}
	release()
	cancel()
	if release, err := d.acquireReusedWorktreeSnapshot(ctx, Task{ID: "cancelled"}, &localDirectoryAssignment{RealPath: path}, d.logger); err == nil || release != nil {
		t.Fatal("cancelled snapshot was tracked")
	}
	if d.resourceWaitTasks.Load() != 0 {
		t.Fatal("snapshot wait accounting leaked")
	}
}
