package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestFailedAutomaticCleanupRetainsOutputsButReclaimsInitialBaseline(t *testing.T) {
	for _, kind := range []string{"baseline", "dirty", "output", "commit", "unknown_baseline", "cancelled_dirty", "changed_after_preview"} {
		t.Run(kind, func(t *testing.T) {
			// Given a failed run with a real managed Git checkout and recorded baseline.
			d := newGCTestDaemon(t, taskLifecycleTestHandler(t, func(string) protocol.TaskGCStatus {
				state := "failed"
				if kind == "cancelled_dirty" {
					state = "cancelled"
				}
				return protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: state, CompletedAt: time.Now(), LifecycleSupported: true, RetentionSupported: true}
			}))
			d.cfg.EnvironmentRecycleEnabled = true
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", nil)
			repo := createWorktreeTestRepo(t)
			wt, err := execenv.PrepareLocalWorktree(execenv.LocalWorktreeParams{LocalPath: repo, EnvRoot: root, AgentName: "test", TaskID: "task", WorkspaceID: "ws1", AgentID: "agent", RuntimeID: "runtime", ConversationID: "task", ConversationKey: "task", RetainCheckout: true}, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "dirty", "cancelled_dirty":
				writeLifecycleFile(t, filepath.Join(wt.Path, "new.txt"), "uncommitted output")
			case "unknown_baseline":
				if err := os.Remove(filepath.Join(root, ".local_worktree_reuse.json")); err != nil {
					t.Fatal(err)
				}
			case "output":
				writeLifecycleFile(t, filepath.Join(root, "output", "report.txt"), "deliverable")
			case "commit":
				writeLifecycleFile(t, filepath.Join(wt.Path, "new.txt"), "committed output")
				worktreeTestGit(t, wt.Path, "add", "new.txt")
				worktreeTestGit(t, wt.Path, "commit", "-m", "agent work")
			}
			ctx := withEnvironmentScope(t.Context(), environmentOperationScope{WorkspaceID: "ws1", RuntimeID: "runtime", Automatic: true})
			// When automatic cleanup previews and executes under the same safety gate.
			preview := d.cleanupUnreferencedEnvironment(ctx, root, "")
			if kind == "changed_after_preview" {
				if preview.Reason != "" {
					t.Fatalf("baseline preview failed: %+v", preview)
				}
				writeLifecycleFile(t, filepath.Join(wt.Path, "late.txt"), "new output")
			}
			result := d.cleanupUnreferencedEnvironment(ctx, root, preview.Revision)
			// Then only a pristine initial baseline can be reclaimed.
			if kind != "baseline" {
				row := ManagedWorktree{TaskID: "task", TaskDiskUsage: TaskDiskUsage{WorkspaceID: "ws1", Path: root}}
				d.describeManagedWorktree(t.Context(), &row)
				if row.NextAction != protocol.WorktreeRetained && row.NextAction != protocol.WorktreeUnknown {
					t.Fatalf("inventory falsely offered cleanup: %+v", row)
				}
			}
			if kind == "baseline" {
				if !result.Reclaimed {
					t.Fatalf("baseline retained: %+v", result)
				}
			} else {
				if result.Reclaimed || result.Reason == "" {
					t.Fatalf("output not protected: %+v", result)
				}
				if _, err := os.Stat(root); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
