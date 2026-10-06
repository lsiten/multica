package daemon

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestEnvironmentRetentionRequiresCurrentTaskSupport(t *testing.T) {
	for _, scenario := range []struct {
		name, issueStatus, category string
		human, superseded, runOnly  bool
		keep                        bool
	}{
		{name: "running_issue", issueStatus: "in_progress", category: "started", keep: true},
		{name: "review", issueStatus: "in_review", category: "started", keep: true},
		{name: "blocked", issueStatus: "blocked", category: "started", keep: true},
		{name: "custom_started", issueStatus: "verification", category: "started", keep: true},
		{name: "done", issueStatus: "done", category: "done"},
		{name: "backlog", issueStatus: "backlog", category: "unstarted"},
		{name: "superseded", issueStatus: "in_progress", category: "started", superseded: true},
		{name: "run_configuration_only", issueStatus: "in_review", category: "started", runOnly: true},
		{name: "idle_chat"},
		{name: "waiting_human_chat", human: true, keep: true},
		{name: "closed_human_request", issueStatus: "done", category: "done", human: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var workdir string
			d := newGCTestDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				status := protocol.TaskGCStatus{WorkspaceID: "ws1", RuntimeID: "runtime", AgentID: "agent", Status: "completed", CompletedAt: time.Now(), LifecycleSupported: true, RetentionSupported: true, CurrentAgent: true, WorkDir: workdir, CurrentWorkDir: workdir, WaitingHuman: scenario.human}
				if scenario.superseded {
					status.CurrentWorkDir = filepath.Join(t.TempDir(), "workdir")
				}
				if scenario.issueStatus != "" {
					status.IssueID, status.IssueStatus, status.IssueStatusCategory = "issue", scenario.issueStatus, scenario.category
				} else {
					status.ChatSessionID = "chat"
				}
				json.NewEncoder(w).Encode(status)
			}))
			d.cfg.GCEnabled, d.cfg.EnvironmentRecycleEnabled, d.cfg.EnvironmentArchiveTTL = true, true, 24*time.Hour
			meta := &execenv.GCMeta{Kind: execenv.GCKindIssue, IssueID: "issue", TaskID: "task", WorkspaceID: "ws1", CompletedAt: time.Now()}
			if scenario.issueStatus == "" {
				meta.Kind, meta.IssueID, meta.ChatSessionID = execenv.GCKindChat, "", "chat"
			}
			root := createTaskDir(t, d.cfg.WorkspacesRoot, "ws1", "task", meta)
			workdir = filepath.Join(root, "workdir")
			if !scenario.runOnly {
				writeLifecycleFile(t, filepath.Join(workdir, "code.txt"), "keep task work")
			}
			eligible, reason := d.automaticCleanupEligible(t.Context(), root, meta)
			if eligible == scenario.keep {
				t.Fatalf("keep=%v eligible=%v reason=%s", scenario.keep, eligible, reason)
			}
		})
	}
}
