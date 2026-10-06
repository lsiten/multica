package daemon

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestReconciliationRecoversMissingCompletionWithoutChangingServerAge(t *testing.T) {
	completed := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Microsecond)
	d := newGCTestDaemon(t, taskLifecycleTestHandler(t, func(string) protocol.TaskGCStatus {
		return protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: "failed", CompletedAt: completed, LifecycleSupported: true, RetentionSupported: true, IssueID: "issue", IssueStatus: "done", IssueStatusCategory: "done"}
	}))
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", nil)
	meta, err := d.reconcileEnvironmentMetadata(t.Context(), root)
	if err != nil || meta.TaskID != "task" || meta.RuntimeID != "runtime" || meta.IssueID != "issue" || !meta.CompletedAt.Equal(completed) {
		t.Fatalf("completion not recovered: %+v %v", meta, err)
	}
	before, err := os.Stat(filepath.Join(root, ".gc_meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.reconcileEnvironmentMetadata(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(root, ".gc_meta.json"))
	if err != nil || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("reconciliation repeatedly rewrote completed history: %v", err)
	}
}

func TestConsumerSurvivingDaemonCrashStillProtectsItsCode(t *testing.T) {
	var running = true
	var workdir string
	d := newGCTestDaemon(t, taskLifecycleTestHandler(t, func(id string) protocol.TaskGCStatus {
		status := protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: "completed", CompletedAt: time.Now().Add(-time.Hour), WorkDir: workdir, CurrentWorkDir: workdir, LifecycleSupported: true, RetentionSupported: true}
		if id == "second" && running {
			status.Status = "running"
		}
		return status
	}))
	d.cfg.EnvironmentRecycleEnabled = true
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "first", &execenv.GCMeta{WorkspaceID: "ws1", TaskID: "first", Kind: execenv.GCKindQuickCreate, RuntimeID: "runtime", AgentID: "agent", CompletedAt: time.Now().Add(-time.Hour)})
	workdir = filepath.Join(root, "workdir")
	writeLifecycleFile(t, filepath.Join(workdir, "code.txt"), "survive daemon crash")
	if err := execenv.UpdateWorktreeConsumer(root, execenv.WorktreeConsumer{TaskID: "second", AgentID: "agent", RuntimeID: "runtime"}, false); err != nil {
		t.Fatal(err)
	}
	meta, err := execenv.ReadGCMeta(root)
	if err != nil {
		t.Fatal(err)
	}
	if eligible, reason := d.automaticCleanupEligible(t.Context(), root, meta); eligible || reason != "active" {
		t.Fatalf("crashed daemon lost live consumer: %v %s", eligible, reason)
	}
	running = false
	if eligible, reason := d.automaticCleanupEligible(t.Context(), root, meta); !eligible {
		t.Fatalf("ended consumer pins checkout forever: %s", reason)
	}
}

func TestDeletedTaskEnvironmentDeletesOutputsWithoutCreatingArchive(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var request struct {
			TaskIDs []string `json:"task_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		response := protocol.TaskGCBatch{Tasks: []protocol.TaskGCStatus{}}
		for _, id := range request.TaskIDs {
			response.Tasks = append(response.Tasks, protocol.TaskGCStatus{TaskID: id, WorkspaceID: "ws1", Missing: true, LifecycleSupported: true, RetentionSupported: true})
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	d.rootCtx = t.Context()
	t.Cleanup(d.stopEnvironmentOperations)
	d.cfg.EnvironmentRecycleEnabled = true
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "deleted", nil)
	writeLifecycleFile(t, filepath.Join(root, "output", "deliverable"), "preserve deleted task output")
	if !d.scheduleAutomaticEnvironmentRecycle(t.Context(), root) {
		t.Fatal("deleted task environment was not scheduled")
	}
	d.environmentOperationWorkers.Wait()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("deleted task directory remained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d.cfg.WorkspacesRoot, ".environment-archive")); !os.IsNotExist(err) {
		t.Fatalf("deleted task left backup data: %v", err)
	}

}
