package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func scopedEnvironmentTestDaemon(t *testing.T) *Daemon {
	d := newGCTestDaemon(t, taskLifecycleTestHandler(t, func(id string) protocol.TaskGCStatus {
		runtime, workspace := "runtime", "ws1"
		if strings.Contains(id, "other") {
			runtime = "other-runtime"
		}
		if strings.Contains(id, "foreign") {
			workspace = "ws2"
		}
		return protocol.TaskGCStatus{Status: "completed", WorkspaceID: workspace, RuntimeID: runtime, AgentID: "agent", LifecycleSupported: true, RetentionSupported: true, CompletedAt: time.Now().Add(-time.Hour)}
	}))
	d.rootCtx = t.Context()
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime"}, "other-runtime": {ID: "other-runtime"}}
	d.workspaces = map[string]*workspaceState{"ws1": {runtimeIDs: []string{"runtime", "other-runtime"}}}
	t.Cleanup(d.stopEnvironmentOperations)
	return d
}

func TestRemoteEnvironmentInventoryUsesAuthoritativeTaskRuntimeScope(t *testing.T) {
	d := scopedEnvironmentTestDaemon(t)
	createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", &execenv.GCMeta{WorkspaceID: "ws1", TaskID: "task", RuntimeID: "wrong-local-diagnostic"})
	createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "other", &execenv.GCMeta{WorkspaceID: "ws1", TaskID: "other", RuntimeID: "runtime"})
	createTaskDir(t, d.cfg.WorkspacesRoot, "ws2", "foreign", &execenv.GCMeta{WorkspaceID: "ws2", TaskID: "foreign"})
	command := protocol.LocalReviewCommand{Action: "environment", WorkspaceID: "ws1", RuntimeID: "runtime", ActorID: "owner", Environment: &protocol.EnvironmentCommand{Action: "inventory"}}
	result := d.runRemoteReview(t.Context(), command)
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	var rows []ManagedWorktree
	if err := json.Unmarshal(result.Page, &rows); err != nil || len(rows) != 1 || rows[0].TaskID != "task" || rows[0].RuntimeID != "runtime" {
		t.Fatalf("scope leak: %+v %v", rows, err)
	}
	command.WorkspaceID = "ws2"
	if result := d.runRemoteReview(t.Context(), command); result.Error == "" {
		t.Fatal("foreign workspace accepted")
	}
}

func TestEnvironmentOperationPersistsReceiptAndNeverReplaysDifferentInput(t *testing.T) {
	d := scopedEnvironmentTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", nil)
	cache := filepath.Join(root, execenv.ManagedReclaimableArtifactSubpaths()[0], "binary")
	writeLifecycleFile(t, cache, "cache")
	scope := environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "runtime"}
	preview := d.worktreeCacheOperation(withEnvironmentScope(t.Context(), scope), root, "")
	request := protocol.EnvironmentOperationRequest{ID: strings.Repeat("a", 64), Action: "clean_cache", Selections: []protocol.EnvironmentSelection{{EnvironmentID: preview.EnvironmentID, Revision: preview.Revision}}}
	if _, err := d.startEnvironmentOperation(scope, request); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := d.environmentOperationStatus(scope, request.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status != "running" {
			if status.Status != "completed" || status.Completed != 1 || len(status.Results) != 1 {
				t.Fatalf("operation failed: %+v", status)
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("operation did not finish")
		case <-ticker.C:
		}
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("cache retained: %v", err)
	}
	d.environmentOperationWorkers.Wait()
	d.environmentOperations = nil
	receipt, err := d.environmentOperationStatus(scope, request.ID, false)
	if err != nil || receipt.Status != "completed" {
		t.Fatalf("durable receipt lost: %+v %v", receipt, err)
	}
	request.Selections[0].Revision = strings.Repeat("b", 64)
	if _, err := d.startEnvironmentOperation(scope, request); err == nil {
		t.Fatal("operation identity accepted different input")
	}
	if _, err := d.environmentOperationStatus(environmentOperationScope{WorkspaceID: "ws2", RuntimeID: "runtime"}, request.ID, false); err == nil {
		t.Fatal("foreign scope read receipt")
	}
}

func TestScopedCacheMutationRejectsRuntimeChangesInsideExecutionGuard(t *testing.T) {
	d := scopedEnvironmentTestDaemon(t)
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "other", &execenv.GCMeta{WorkspaceID: "ws1", TaskID: "other"})
	cache := filepath.Join(root, execenv.ManagedReclaimableArtifactSubpaths()[0], "binary")
	writeLifecycleFile(t, cache, "keep")
	result := d.worktreeCacheOperation(withEnvironmentScope(t.Context(), environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "runtime"}), root, strings.Repeat("a", 64))
	if result.Reason != "scope_changed" || result.RemovedCount != 0 {
		t.Fatalf("scope bypass: %+v", result)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatal(err)
	}
}
