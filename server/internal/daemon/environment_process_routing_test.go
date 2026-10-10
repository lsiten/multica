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

// TestEnvironmentCommandRoutesPhysicalActionToChild verifies F1 item 1: when the
// environment capability is split into a child service, the control parent
// routes a physical (Git/filesystem) environment action to the child instead of
// performing the work itself. The routed result is returned as raw JSON and
// re-encoded by the caller, preserving the wire shape. A control-side action
// must never take this path.
func TestEnvironmentCommandRoutesPhysicalActionToChild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, _, cleanup := newLeaderReuseTestDaemon(t)
	t.Cleanup(cleanup)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/me" {
			if r.Header.Get("Authorization") != "Bearer owned-parent" {
				http.Error(w, "unauthorized", 403)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "owned-account"})
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(backend.Close)
	d.client = NewClient(backend.URL)
	d.client.token = "owned-parent"
	d.cfg.ServerBaseURL = backend.URL
	d.cfg.DaemonID = "owned-environment-daemon"
	d.cfg.ProcessServices = []string{"environment"}
	d.cfg.AgentTimeout = 15 * time.Second
	d.cfg.NativeHostBuild = "fixture/commit"
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.NativeHostExecutable = executable
	profile, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.NativeVscreenPreferencesPath = filepath.Join(profile, "vscreen.json")
	root, err := filepath.EvalSymlinks(d.cfg.WorkspacesRoot)
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.WorkspacesRoot = root
	d.localPathLocks = NewLocalPathLocker()
	d.repoCache = &environmentRepoCache{daemon: d}
	d.workspaces["ws-leader"] = &workspaceState{workspaceID: "ws-leader", runtimeIDs: []string{"rt-leader"}, allowedRepoURLs: map[string]struct{}{}}
	t.Cleanup(func() {
		if d.environmentProcess != nil {
			if err := d.environmentProcess.close(); err != nil {
				t.Error(err)
			}
			t.Log("CLEANUP routing child closed/reaped")
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	scope := environmentOperationScope{WorkspaceID: "ws-leader", RuntimeID: "rt-leader"}

	value, err := d.executeEnvironmentCommand(ctx, scope, protocol.EnvironmentCommand{Action: "inventory"})
	if err != nil {
		t.Fatalf("physical inventory routed to child: %v", err)
	}
	if d.environmentProcess == nil {
		t.Fatal("physical action did not route to the environment child")
	}
	raw, ok := value.(json.RawMessage)
	if !ok {
		t.Fatalf("routed result is not raw JSON: %T", value)
	}
	if strings.TrimSpace(string(raw)) == "" {
		t.Fatal("child produced an empty inventory result")
	}
	// The child reports its own PID; a real child differs from the parent.
	inventory, err := d.environmentProcess.process.Client.Read(ctx, "environment.inventory", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var child struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(inventory, &child); err != nil || child.PID == 0 || child.PID == os.Getpid() {
		t.Fatalf("physical action did not run in the child process: %+v %v", child, err)
	}
	t.Logf("OBSERVE physical action routed to childPID=%d parentPID=%d", child.PID, os.Getpid())
}

// TestEnvironmentOperationRoutesPhysicalMutationToChild verifies F1 item 2: when
// the environment capability is split into a child service, the control parent
// delegates the actual physical (Git/filesystem) environment operation
// (clean_cache/archive/cleanup/discard/restore) to the child instead of running
// it itself. This closes the gap where only preview actions (and review) were
// routed but the real operation ran in the control parent, which would make the
// parent a physical Git/worktree writer. The parent must launch the child and
// the child must be the process that performs the mutation.
func TestEnvironmentOperationRoutesPhysicalMutationToChild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, _, cleanup := newLeaderReuseTestDaemon(t)
	t.Cleanup(cleanup)
	facts := func(id string) protocol.TaskGCStatus {
		return protocol.TaskGCStatus{Status: "completed", WorkspaceID: "ws-leader", RuntimeID: "rt-leader", AgentID: "agent", LifecycleSupported: true, RetentionSupported: true, CompletedAt: time.Now().Add(-time.Hour)}
	}
	backend := httptest.NewServer(funcHandler(t, facts))
	t.Cleanup(backend.Close)
	d.client = NewClient(backend.URL)
	d.client.token = "owned-parent"
	d.cfg.ServerBaseURL = backend.URL
	d.cfg.DaemonID = "owned-environment-daemon"
	d.cfg.ProcessServices = []string{"environment"}
	d.cfg.AgentTimeout = 15 * time.Second
	d.cfg.NativeHostBuild = "fixture/commit"
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.NativeHostExecutable = executable
	profile, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.NativeVscreenPreferencesPath = filepath.Join(profile, "vscreen.json")
	root, err := filepath.EvalSymlinks(d.cfg.WorkspacesRoot)
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.WorkspacesRoot = root
	d.localPathLocks = NewLocalPathLocker()
	d.repoCache = &environmentRepoCache{daemon: d}
	d.workspaces["ws-leader"] = &workspaceState{workspaceID: "ws-leader", runtimeIDs: []string{"rt-leader"}, allowedRepoURLs: map[string]struct{}{}}
	d.runtimeIndex = map[string]Runtime{"rt-leader": {ID: "rt-leader", Provider: "claude"}}
	t.Cleanup(func() {
		if d.environmentProcess != nil {
			if err := d.environmentProcess.close(); err != nil {
				t.Error(err)
			}
			t.Log("CLEANUP operation routing child closed/reaped")
		}
	})

	// A real managed worktree with a reclaimable cache artifact so the control
	// parent resolves the selection path and delegates the physical clean_cache to
	// the owned child instead of running it locally.
	taskRoot := createTaskDir(t, d.cfg.WorkspacesRoot, "ws-leader", "task-op", nil)
	cache := filepath.Join(taskRoot, execenv.ManagedReclaimableArtifactSubpaths()[0], "binary")
	writeLifecycleFile(t, cache, "cache to reclaim")
	taskOwner, err := d.gcTaskDirOwner(taskRoot)
	if err != nil {
		t.Fatal(err)
	}
	envID := d.managedEnvironmentID(taskRoot, "ws-leader", taskOwner.TaskID)

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	scope := environmentOperationScope{WorkspaceID: "ws-leader", RuntimeID: "rt-leader"}
	request := protocol.EnvironmentOperationRequest{ID: strings.Repeat("a", 64), Action: "clean_cache", Selections: []protocol.EnvironmentSelection{{EnvironmentID: envID, Revision: strings.Repeat("c", 64)}}}
	if _, err := d.startEnvironmentOperation(scope, request); err != nil {
		t.Fatalf("start operation: %v", err)
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		status, err := d.environmentOperationStatus(scope, request.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status != "running" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("operation did not settle: %+v", status)
		case <-time.After(50 * time.Millisecond):
		}
	}
	finalStatus, err := d.environmentOperationStatus(scope, request.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("OBSERVE clean_cache settled status=%s results=%d", finalStatus.Status, len(finalStatus.Results))
	if d.environmentProcess == nil {
		t.Fatalf("operation did not route to the environment child")
	}
	// The child is a distinct process from the control parent; it was launched
	// only by the operation routing path, so a live child proves delegation. A
	// real child differs from the parent PID.
	inventory, err := d.environmentProcess.process.Client.Read(ctx, "environment.inventory", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("child identity read: %v", err)
	}
	var child struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(inventory, &child); err != nil || child.PID == 0 || child.PID == os.Getpid() {
		t.Fatalf("operation did not run in the child process: %+v %v", child, err)
	}
	t.Logf("OBSERVE clean_cache operation routed to childPID=%d parentPID=%d", child.PID, os.Getpid())
}

// funcHandler adapts a task-lifecycle facts function into an http handler that
// also answers /api/me, so the control parent can both resolve task GC status and
// fetch the environment owner identity.
func funcHandler(t *testing.T, facts func(string) protocol.TaskGCStatus) http.Handler {
	lifecycle := taskLifecycleTestHandler(t, facts)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/me" {
			if r.Header.Get("Authorization") != "Bearer owned-parent" {
				http.Error(w, "unauthorized", 403)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "owned-account"})
			return
		}
		lifecycle.ServeHTTP(w, r)
	})
}
