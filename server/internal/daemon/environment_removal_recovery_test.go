package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestInterruptedDeletionResumesWithoutTouchingRecreatedRoot(t *testing.T) {
	d := newGCTestDaemon(t, taskLifecycleTestHandler(t, func(string) protocol.TaskGCStatus {
		return protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: "completed", CompletedAt: time.Now(), LifecycleSupported: true, RetentionSupported: true}
	}))
	root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", nil)
	writeLifecycleFile(t, filepath.Join(root, "output", "old-file"), "delete interrupted old content")
	trash := filepath.Join(d.cfg.WorkspacesRoot, ".environment-trash")
	if err := os.MkdirAll(trash, 0700); err != nil {
		t.Fatal(err)
	}
	owner, err := d.gcTaskDirOwner(root)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(d.cfg.WorkspacesRoot, root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(execenv.EnvironmentRemovalReceipt{Owner: *owner, RelativeRoot: filepath.ToSlash(relative)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trash, "cleanup-test.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, filepath.Join(trash, "cleanup-test")); err != nil {
		t.Fatal(err)
	}
	newRoot := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", nil)
	writeLifecycleFile(t, filepath.Join(newRoot, "output", "new-file"), "keep new execution content")
	d.resumeInterruptedEnvironmentRemovals(t.Context())
	if _, err := os.Stat(filepath.Join(trash, "cleanup-test")); !os.IsNotExist(err) {
		t.Fatalf("interrupted deletion remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trash, "cleanup-test.json")); !os.IsNotExist(err) {
		t.Fatalf("completed deletion receipt remains: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(newRoot, "output", "new-file")); err != nil || string(data) != "keep new execution content" {
		t.Fatalf("recreated root changed: %q %v", data, err)
	}
}
