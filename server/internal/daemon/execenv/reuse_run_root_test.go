package execenv

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestReuseSeparatesRunConfigurationFromSharedCodeAndCleanup(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := t.TempDir()
	first, err := Prepare(PrepareParams{WorkspacesRoot: root, WorkspaceID: "workspace", TaskID: "first", RuntimeID: "runtime", Provider: "claude", Task: TaskContextForEnv{AgentID: "agent", IssueID: "issue", DisabledRuntimeSkills: []RuntimeSkillRefForEnv{{Key: "first-disabled-skill"}}}}, logger)
	if err != nil {
		t.Fatal(err)
	}
	first.ReleaseLock()
	code := filepath.Join(first.WorkDir, "code.txt")
	if err := os.WriteFile(code, []byte("keep code"), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(first.MulticaConfigRoot, "config.json")
	if err := os.WriteFile(config, []byte("keep first configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimEnvRoot(RootDirParams{WorkspacesRoot: root, WorkspaceID: "workspace", TaskID: "second"})
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	second := Reuse(ReuseParams{WorkspacesRoot: root, WorkDir: first.WorkDir, RunRoot: claim.RootDir(), Provider: "claude", Task: TaskContextForEnv{AgentID: "agent", IssueID: "issue", DisabledRuntimeSkills: []RuntimeSkillRefForEnv{{Key: "disabled-skill"}}}}, logger)
	if second == nil || second.RootDir != claim.RootDir() || second.CodeRootDir != first.RootDir || second.MulticaConfigRoot == first.MulticaConfigRoot {
		t.Fatalf("configuration and code not separated: %+v", second)
	}
	if err := second.Cleanup(false); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(code); err != nil || string(data) != "keep code" {
		t.Fatalf("per-run cleanup deleted shared code: %q %v", data, err)
	}
	if data, err := os.ReadFile(config); err != nil || string(data) != "keep first configuration" {
		t.Fatalf("prior configuration changed: %q %v", data, err)
	}
	if _, err := os.Stat(first.ClaudeSettingsPath); err != nil {
		t.Fatalf("prior provider settings were removed on reuse: %v", err)
	}
	if err := CleanupSidecars(first.RootDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(second.ClaudeSettingsPath); err != nil {
		t.Fatalf("code sidecar cleanup removed another run's provider settings: %v", err)
	}
	if reused := Reuse(ReuseParams{WorkspacesRoot: root, WorkDir: first.WorkDir, RunRoot: t.TempDir(), Provider: "claude"}, logger); reused != nil {
		t.Fatal("unowned run configuration root was accepted")
	}
}
