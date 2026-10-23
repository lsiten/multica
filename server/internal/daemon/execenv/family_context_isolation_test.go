package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFamilyManagedRunsKeepPrivateContextWhileSharingCode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	params := PrepareParams{
		WorkspacesRoot: t.TempDir(), WorkspaceID: "ws", RuntimeID: "runtime", TaskID: turnOneTask,
		Provider: "claude", IsolateLocalContext: true,
		Task: TaskContextForEnv{IssueID: "child-one", AgentID: "agent-one", CodeWorkspaceIssueID: "root", AgentSkills: []SkillContextForEnv{{Name: "Task skill", Content: "first private skill"}}},
	}
	first, err := Prepare(params, worktreeTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer first.ReleaseLock()
	writeFile(t, filepath.Join(first.WorkDir, "shared.txt"), "family code")
	if first.ContextDir == "" || first.ContextDir == first.WorkDir {
		t.Fatal("first managed family run wrote context into shared code")
	}
	firstMarker := readFile(t, filepath.Join(first.ContextDir, TaskContextMarkerRelPath))
	secondRoot, err := ClaimEnvRoot(RootDirParams{WorkspacesRoot: params.WorkspacesRoot, WorkspaceID: "ws", TaskID: turnTwoTask})
	if err != nil {
		t.Fatal(err)
	}
	defer secondRoot.Release()
	secondTask := params.Task
	secondTask.AgentID, secondTask.IssueID = "agent-two", "child-two"
	secondTask.AgentSkills = []SkillContextForEnv{{Name: "Task skill", Content: "second private skill"}}
	second := Reuse(ReuseParams{WorkspacesRoot: params.WorkspacesRoot, RunRoot: secondRoot.RootDir(), WorkDir: first.WorkDir, Provider: "claude", IsolateLocalContext: true, Task: secondTask}, worktreeTestLogger())
	if second == nil {
		t.Fatal("second managed family run could not reuse shared code")
	}
	if second.WorkDir != first.WorkDir || second.ContextDir == "" || second.ContextDir == first.ContextDir {
		t.Fatalf("code/context separation failed: first=%+v second=%+v", first, second)
	}
	if got := readFile(t, filepath.Join(second.WorkDir, "shared.txt")); got != "family code" {
		t.Fatal("shared code did not survive sibling reuse")
	}
	for _, run := range []struct {
		env  *Environment
		want string
	}{{first, "first private skill"}, {second, "second private skill"}} {
		if got := readFile(t, filepath.Join(skillsDirPath(run.env.ContextDir, "claude"), "task-skill", "SKILL.md")); !strings.Contains(got, run.want) {
			t.Fatalf("run-private skill changed: %q", got)
		}
	}
	if got := readFile(t, filepath.Join(first.ContextDir, TaskContextMarkerRelPath)); got != firstMarker {
		t.Fatal("second run replaced first run's task identity")
	}
	if err := CleanupSidecars(second.RootDir); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(skillsDirPath(first.ContextDir, "claude"), "task-skill", "SKILL.md")); !strings.Contains(got, "first private skill") {
		t.Fatal("sibling cleanup removed live run's skills")
	}
	for _, path := range []string{TaskContextMarkerRelPath, filepath.Join(".claude", "skills"), ".agent_context"} {
		if _, err := os.Stat(filepath.Join(first.WorkDir, path)); !os.IsNotExist(err) {
			t.Fatalf("private sidecar appeared in shared code at %s: %v", path, err)
		}
	}
}
