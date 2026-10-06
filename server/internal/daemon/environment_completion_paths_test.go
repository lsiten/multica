package daemon

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestHandleTaskFailureRecordsBothRunAndSharedCodeCompletion(t *testing.T) {
	d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"status":"running"}`))
		if err != nil {
			t.Error(err)
		}
	}))
	d.runtimeIndex = map[string]Runtime{"runtime": {ID: "runtime", Provider: "claude"}}
	task := Task{ID: "second", WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", IssueID: "issue"}
	code := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "first", nil)
	var run string
	d.runner = taskRunnerFunc(func(context.Context, Task, string, int, *slog.Logger) (TaskResult, error) {
		claim, err := execenv.ClaimEnvRoot(taskRootDirParams(d.cfg.WorkspacesRoot, task))
		if err != nil {
			t.Fatal(err)
		}
		defer claim.Release()
		run = claim.RootDir()
		return TaskResult{EnvRoot: run, CodeRoot: code, WorkDir: filepath.Join(code, "workdir")}, errors.New("fixture execution failed")
	})
	d.handleTask(t.Context(), task, 0)
	for _, path := range []string{run, code} {
		meta, err := execenv.ReadGCMeta(path)
		if err != nil || meta.AgentID != task.AgentID || meta.RuntimeID != task.RuntimeID || meta.CompletedAt.IsZero() || meta.AutoCleanup {
			t.Fatalf("failed execution missed completion at %s: %+v %v", path, meta, err)
		}
		if path == code && (meta.TaskID != "first" || meta.LatestTaskID != task.ID) {
			t.Fatalf("failure rewrote shared ownership: %+v", meta)
		}
	}
}
