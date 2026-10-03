package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestManagedWorkdirReuseRequiresSameProjectSquadRuntimeAndRepositoryScope(t *testing.T) {
	for _, change := range []string{"same", "project", "squad", "runtime", "repository", "legacy"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			workdir := filepath.Join(root, "ws-leader", "12345678", "workdir")
			writeLeaderTaskMarker(t, workdir, "agent-leader", "issue-leader")
			writeLeaderManagedEnvProvenance(t, workdir, "ws-leader", "issue-leader", "agent-leader")
			task := leaderReuseTestTask("followup")
			task.PriorWorkDir = workdir
			switch change {
			case "project":
				task.ProjectID = "other"
			case "squad":
				task.SquadID = "other"
			case "runtime":
				task.RuntimeID = "other"
			case "repository":
				task.Repos = []RepoData{{URL: "https://example.test/other"}}
			case "legacy":
				if err := os.WriteFile(filepath.Join(filepath.Dir(workdir), ".managed_env.json"), []byte(`{"managed_by":"`+execenv.ManagedEnvProvenanceManagedBy+`","workspace_id":"ws-leader","agent_id":"agent-leader","issue_id":"issue-leader"}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, reusable := shouldReusePriorWorkdir(task, nil, root)
			if reusable != (change == "same") {
				t.Fatalf("%s reuse=%v", change, reusable)
			}
		})
	}
}

func TestAutomationRunsReuseCodeOnlyWithinTheirWorkline(t *testing.T) {
	d, argsFile, cleanup := newLeaderReuseTestDaemon(t)
	defer cleanup()
	first := leaderReuseTestTask("task-first")
	first.IssueID, first.IsLeaderTask = "", false
	first.AutopilotID, first.AutopilotRunID = "automation", "run-first"
	result, err := d.runTask(context.Background(), first, "claude", 0, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	writeLifecycleFile(t, filepath.Join(result.WorkDir, "code.txt"), "carry code")
	writeLifecycleFile(t, filepath.Join(result.EnvRoot, "multica-config", "retained-config"), "previous task configuration")
	second := first
	second.ID, second.AutopilotRunID, second.PriorWorkDir = "task-second", "run-second", result.WorkDir
	resumed, err := d.runTask(context.Background(), second, "claude", 0, d.logger)
	if err != nil || !sameDir(t, resumed.WorkDir, result.WorkDir) {
		t.Fatalf("automation workdir was not reused: %+v %v", resumed, err)
	}
	if sameDir(t, resumed.EnvRoot, result.EnvRoot) || !sameDir(t, resumed.CodeRoot, result.EnvRoot) {
		t.Fatalf("per-run configuration was not isolated from reused code: %+v", resumed)
	}
	if data, err := os.ReadFile(filepath.Join(result.EnvRoot, "multica-config", "retained-config")); err != nil || string(data) != "previous task configuration" {
		t.Fatalf("prior task configuration changed: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(resumed.WorkDir, "code.txt")); err != nil || string(data) != "carry code" {
		t.Fatalf("automation code lost: %q %v", data, err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil || strings.Contains(string(args), "--resume") {
		t.Fatalf("separate automation run inherited a provider session: %s %v", args, err)
	}
	second.ID, second.AutopilotID = "task-other", "other-automation"
	other, err := d.runTask(context.Background(), second, "claude", 0, d.logger)
	if err != nil || sameDir(t, other.WorkDir, result.WorkDir) {
		t.Fatalf("automation worklines crossed: %+v %v", other, err)
	}
}
