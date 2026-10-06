package daemon

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestReusedEnvironmentRecyclingFollowsLatestTaskAndPreservesOriginalOwner(t *testing.T) {
	var directory, latestState atomic.Value
	directory.Store("")
	latestState.Store("running")
	d := newGCTestDaemon(t, taskLifecycleTestHandler(t, func(id string) protocol.TaskGCStatus {
		status := protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: "completed", LifecycleSupported: true, RetentionSupported: true, CompletedAt: time.Now().Add(-time.Hour), IssueID: "issue", IssueStatusCategory: "done"}
		if id == "second" {
			status.Status = latestState.Load().(string)
			status.WorkDir = directory.Load().(string)
		}
		return status
	}))
	d.rootCtx = t.Context()
	t.Cleanup(d.stopEnvironmentOperations)
	d.cfg.GCEnabled, d.cfg.EnvironmentRecycleEnabled = true, true
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	d.workspaces = map[string]*workspaceState{"ws1": {runtimeIDs: []string{"runtime"}}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "first", &execenv.GCMeta{WorkspaceID: "ws1", TaskID: "first", Kind: execenv.GCKindIssue})
	workdir := filepath.Join(root, "workdir")
	writeLifecycleFile(t, filepath.Join(workdir, "code.txt"), "retain changed code")
	directory.Store(workdir)
	meta, ok, err := d.gcMetaForTaskRoot(Task{ID: "second", WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", IssueID: "issue"}, root)
	if err != nil || !ok || meta.TaskID != "first" || meta.LatestTaskID != "second" {
		t.Fatalf("completion changed physical ownership: %+v %v", meta, err)
	}
	if err := execenv.WriteGCMeta(root, meta, d.logger); err != nil {
		t.Fatal(err)
	}
	if d.scheduleAutomaticEnvironmentRecycle(t.Context(), root) {
		t.Fatal("running successor was reclaimed using its completed predecessor")
	}
	latestState.Store("completed")
	if !d.scheduleAutomaticEnvironmentRecycle(t.Context(), root) {
		t.Fatal("completed successor was retained because it reused another task's root")
	}
	d.environmentOperationWorkers.Wait()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("reused root remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d.cfg.WorkspacesRoot, ".environment-archive")); !os.IsNotExist(err) {
		t.Fatalf("unused shared root left an archive: %v", err)
	}

}

func TestLatestEnvironmentTaskRequiresMatchingRuntimeAgentAndDirectory(t *testing.T) {
	for _, mismatch := range []string{"workspace", "runtime", "agent", "directory", "legacy_original_mismatch"} {
		t.Run(mismatch, func(t *testing.T) {
			var directory atomic.Value
			directory.Store("")
			d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				status := protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", WorkDir: directory.Load().(string), Status: "completed", LifecycleSupported: true}
				switch mismatch {
				case "workspace":
					status.WorkspaceID = "other"
				case "runtime":
					status.RuntimeID = "other"
				case "agent":
					status.AgentID = "other"
				case "legacy_original_mismatch":
					if strings.Contains(r.URL.Path, "/first/") {
						status.RuntimeID = "other"
					}
				case "directory":
					status.WorkDir = filepath.Join(t.TempDir(), "workdir")
				}
				json.NewEncoder(w).Encode(status)
			}))
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "first", nil)
			directory.Store(filepath.Join(root, "workdir"))
			owner, err := d.gcTaskDirOwner(root)
			if err != nil {
				t.Fatal(err)
			}
			meta := &execenv.GCMeta{WorkspaceID: "ws1", TaskID: "first", LatestTaskID: "second", RuntimeID: "runtime", AgentID: "agent", CompletedAt: time.Now()}
			if mismatch == "legacy_original_mismatch" {
				meta.RuntimeID = ""
			}
			if _, err := d.environmentTaskGCStatus(t.Context(), root, owner, meta); err == nil {
				t.Fatal("unproven latest-task pointer was accepted")
			}
		})
	}
}

func TestFailedAndCancelledReusedRunsUpdateCompletionMetadata(t *testing.T) {
	for _, state := range []string{"failed", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			d := worktreeTestDaemon(t)
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "first", nil)
			task := Task{ID: "second", WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", IssueID: "issue"}
			if scheduled := d.recordEnvironmentCompletion(task, TaskResult{Status: state, EnvRoot: root}, d.logger); scheduled != "" {
				t.Fatalf("unsuccessful run opted into legacy successful-run cleanup: %s", scheduled)
			}
			meta, err := execenv.ReadGCMeta(root)
			if err != nil || meta.TaskID != "first" || meta.LatestTaskID != "second" || meta.CompletedAt.IsZero() || meta.AutoCleanup {
				t.Fatalf("failure/cancellation left stale ownership metadata: %+v %v", meta, err)
			}
		})
	}
}

func TestCompletionRecordsBothRunAndSharedCodeRoots(t *testing.T) {
	d := worktreeTestDaemon(t)
	code := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "first", nil)
	run := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "second", nil)
	task := Task{ID: "second", WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", IssueID: "issue"}
	if root := d.recordEnvironmentCompletion(task, TaskResult{Status: "completed", EnvRoot: run, CodeRoot: code}, d.logger); root != run {
		t.Fatalf("wrong per-run cleanup root: %s", root)
	}
	for _, path := range []string{run, code} {
		meta, err := execenv.ReadGCMeta(path)
		if err != nil || meta.CompletedAt.IsZero() || meta.AgentID != "agent" {
			t.Fatalf("completion missing at %s: %+v %v", path, meta, err)
		}
		if path == code && (meta.TaskID != "first" || meta.LatestTaskID != "second") {
			t.Fatalf("shared root lost latest-run binding: %+v", meta)
		}
		if path == run && (meta.TaskID != "second" || meta.LatestTaskID != "") {
			t.Fatalf("run root lost its own binding: %+v", meta)
		}
	}
}
