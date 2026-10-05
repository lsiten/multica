package execenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolatedLocalContextsDoNotOverwriteSharedInstructionsOrSkills(t *testing.T) {
	code := t.TempDir()
	writeFile(t, filepath.Join(code, "CLAUDE.md"), "user instructions\n")
	params := PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "workspace", TaskID: turnOneTask, Provider: "claude", LocalWorkDir: code, IsolateLocalContext: true, Task: TaskContextForEnv{AgentID: "first", IssueID: "first-issue", AgentSkills: []SkillContextForEnv{{Name: "Task skill", Content: "first skill"}}}}
	first, err := Prepare(params, worktreeTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	brief, err := InjectIsolatedRuntimeConfig(first.ContextDir, params.Provider, params.Task)
	if err != nil || !strings.Contains(brief, first.ContextDir) || !strings.Contains(brief, filepath.Join(first.ContextDir, ".multica", "project", "resources.json")) {
		t.Fatalf("isolated context is not discoverable: %s %v", brief, err)
	}
	firstSkill := filepath.Join(first.ContextDir, ".claude", "skills", "task-skill", "SKILL.md")
	before, err := os.ReadFile(firstSkill)
	if err != nil {
		t.Fatal(err)
	}
	params.TaskID, params.Task.AgentID, params.Task.IssueID = turnTwoTask, "second", "second-issue"
	params.Task.AgentSkills[0].Content = "second skill"
	second, err := Prepare(params, worktreeTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	if second.WorkDir != first.WorkDir || second.ContextDir == first.ContextDir {
		t.Fatal("shared code did not retain separate task contexts")
	}
	if err := CleanupSidecars(second.RootDir); err != nil {
		t.Fatal(err)
	}
	if after, err := os.ReadFile(firstSkill); err != nil || string(after) != string(before) {
		t.Fatalf("second task cleanup changed first task skills: %q %v", after, err)
	}
	if got := readFile(t, filepath.Join(code, "CLAUDE.md")); got != "user instructions\n" {
		t.Fatalf("shared instruction file changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(code, ".claude")); !os.IsNotExist(err) {
		t.Fatal("isolated skills leaked into code")
	}
	if err := CleanupSidecars(first.RootDir); err != nil {
		t.Fatal(err)
	}
	first.ReleaseLock()
	second.ReleaseLock()
}

func TestSharedTaskGuardPreservesExistingMarker(t *testing.T) {
	path := t.TempDir()
	marker := filepath.Join(path, TaskContextMarkerRelPath)
	if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
		t.Fatal(err)
	}
	previous := `{"managed_by":"multica-daemon-task","agent_id":"original"}`
	if err := os.WriteFile(marker, []byte(previous), 0600); err != nil {
		t.Fatal(err)
	}
	release, err := ProtectSharedLocalDirectory(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if current := readFile(t, marker); strings.Contains(current, "original") {
		t.Fatal("shared guard attributed another task to the prior agent")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(marker)
	if err != nil || info.Mode().Perm() != 0600 || readFile(t, marker) != previous {
		t.Fatalf("shared guard did not restore the original marker and mode: %v %v", info, err)
	}
}
