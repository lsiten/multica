package daemon

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestLocalReviewInventoryUsesTaskRuntimeBinding(t *testing.T) {
	for _, tc := range []struct{ name, task, workspace, want string }{
		{"matching", "task1", "ws1", "runtime1"},
		{"stale task", "old-task", "ws1", ""},
		{"other workspace", "task1", "other-ws", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := worktreeTestDaemon(t)
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task1", nil)
			if err := execenv.WriteReviewRuntime(root, execenv.ReviewRuntime{TaskID: tc.task, WorkspaceID: tc.workspace, RuntimeID: "runtime1", AgentID: "business-agent", AgentName: "Business Agent"}); err != nil {
				t.Fatal(err)
			}
			rows, err := d.managedWorktrees(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].RuntimeID != tc.want {
				t.Fatalf("runtime inventory = %+v", rows)
			}
			if tc.want != "" && (rows[0].AgentID != "business-agent" || rows[0].AgentName != "Business Agent") {
				t.Fatal("business agent attribution missing", rows[0])
			}
			if tc.want == "" && rows[0].AgentID != "" {
				t.Fatal("stale binding supplied agent attribution", rows[0])
			}
		})
	}
}
