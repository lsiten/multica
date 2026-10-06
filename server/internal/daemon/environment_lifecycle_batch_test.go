package daemon

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestReconciliationOfLargeInventoryUsesBoundedLifecycleBatches(t *testing.T) {
	var batches, points atomic.Int32
	facts := taskLifecycleTestHandler(t, func(string) protocol.TaskGCStatus {
		return protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: "running", LifecycleSupported: true, RetentionSupported: true}
	})
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			batches.Add(1)
		} else {
			points.Add(1)
		}
		facts.ServeHTTP(w, r)
	}))
	d.cfg.EnvironmentRecycleEnabled = true
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	d.workspaces = map[string]*workspaceState{"ws1": {runtimeIDs: []string{"runtime"}}}
	for index := range 1001 {
		id := fmt.Sprintf("%04d", index)
		createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "run-"+id, &execenv.GCMeta{WorkspaceID: "ws1", TaskID: id, RuntimeID: "runtime", AgentID: "agent", Kind: execenv.GCKindIssue, CompletedAt: time.Now().Add(-time.Hour)})
	}
	d.scanAutomaticEnvironmentRecycle(t.Context())
	if batches.Load() != 3 || points.Load() != 0 {
		t.Fatalf("1001 directories used %d batches and %d point requests", batches.Load(), points.Load())
	}
	roots, err := d.environmentRootPaths(t.Context())
	if err != nil || len(roots) != 1001 {
		t.Fatalf("live task directories removed: %d %v", len(roots), err)
	}
}

func TestInventoryBatchesLifecycleAndOmitsEndedConsumers(t *testing.T) {
	var batches, points atomic.Int32
	facts := taskLifecycleTestHandler(t, func(id string) protocol.TaskGCStatus {
		status := protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: "completed", LifecycleSupported: true, RetentionSupported: true}
		if id == "live" {
			status.Status = "running"
		}
		return status
	})
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			batches.Add(1)
		} else {
			points.Add(1)
		}
		facts.ServeHTTP(w, r)
	}))
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	d.workspaces = map[string]*workspaceState{"ws1": {runtimeIDs: []string{"runtime"}}}
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "owner", &execenv.GCMeta{WorkspaceID: "ws1", TaskID: "owner", RuntimeID: "runtime", AgentID: "agent"})
	for _, id := range []string{"live", "ended"} {
		if err := execenv.UpdateWorktreeConsumer(root, execenv.WorktreeConsumer{TaskID: id, AgentID: "agent", RuntimeID: "runtime"}, false); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := d.managedWorktrees(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v error=%v", rows, err)
	}
	if batches.Load() != 1 || points.Load() != 0 {
		t.Fatalf("inventory used %d batches and %d point requests", batches.Load(), points.Load())
	}
	if len(rows[0].ConsumerTaskIDs) != 1 || rows[0].ConsumerTaskIDs[0] != "live" || rows[0].RetainedTaskID != "live" || !rows[0].Active {
		t.Fatalf("incorrect consumers: %+v", rows[0])
	}
}

func TestInventoryMarksAuthoritativelyMissingTaskForCleanup(t *testing.T) {
	d := newGCTestDaemon(t, taskLifecycleTestHandler(t, func(string) protocol.TaskGCStatus {
		return protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", Missing: true, LifecycleSupported: true, RetentionSupported: true}
	}))
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}}
	d.workspaces = map[string]*workspaceState{"ws1": {runtimeIDs: []string{"runtime"}}}
	createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "deleted", &execenv.GCMeta{WorkspaceID: "ws1", TaskID: "deleted", RuntimeID: "runtime"})
	rows, err := d.managedWorktrees(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v error=%v", rows, err)
	}
	if rows[0].NextAction != protocol.WorktreeCleanup || rows[0].RetentionReason != "" {
		t.Fatalf("missing task not reclaimable: %+v", rows[0])
	}
}
