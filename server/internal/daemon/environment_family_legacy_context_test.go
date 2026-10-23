package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

func TestFamilyLegacyRootAdoptionRemovesOldContextAndKeepsCode(t *testing.T) {
	for _, stage := range []string{"normal", "interrupted-cleanup", "interrupted-publication"} {
		t.Run(stage, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			d, _, cleanup := newLeaderReuseTestDaemon(t)
			defer cleanup()
			root := leaderReuseTestTask("legacy-root")
			params := execenv.PrepareParams{WorkspacesRoot: d.cfg.WorkspacesRoot, WorkspaceID: root.WorkspaceID, TaskID: root.ID, RuntimeID: root.RuntimeID, Provider: "omp", McpConfig: []byte(`{"mcpServers":{"old":{"command":"test-only","env":{"TOKEN":"legacy-private"}}}}`),
				Task: execenv.TaskContextForEnv{IssueID: root.IssueID, AgentID: root.AgentID, AgentSkills: []execenv.SkillContextForEnv{{Name: "Old skill", Content: "old private instructions"}}}}
			env, err := execenv.Prepare(params, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			defer env.ReleaseLock()
			writeLifecycleFile(t, filepath.Join(env.WorkDir, "code.txt"), "uncommitted family code")
			writeLifecycleFile(t, filepath.Join(env.WorkDir, "AGENTS.md"), "user instructions")
			if _, err := execenv.InjectRuntimeConfig(env.WorkDir, "omp", params.Task); err != nil {
				t.Fatal(err)
			}
			env.ReleaseLock()
			if stage != "normal" {
				if err := execenv.CleanupLegacyFamilyContext(env.RootDir, env.WorkDir); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "interrupted-publication" {
				scope, err := execenv.ReadManagedEnvProvenance(env.RootDir)
				if err != nil {
					t.Fatal(err)
				}
				scope.CodeWorkspaceIssueID = root.IssueID
				if err := execenv.WriteManagedEnvProvenance(env.RootDir, *scope); err != nil {
					t.Fatal(err)
				}
			}
			child := root
			child.ID, child.IssueID, child.AgentID = "new-child", "child", "child-agent"
			child.Agent = &AgentData{ID: child.AgentID, Name: "child"}
			child.CodeWorkspaceIssueID, child.CodeWorkspaceWorkDir = root.IssueID, env.WorkDir
			result, err := d.runTask(t.Context(), child, "claude", 0, d.logger)
			if err != nil {
				t.Fatal(err)
			}
			if !sameDir(t, result.WorkDir, env.WorkDir) {
				t.Fatal("legacy root code was abandoned")
			}
			for path, want := range map[string]string{"code.txt": "uncommitted family code", "AGENTS.md": "user instructions"} {
				got, err := os.ReadFile(filepath.Join(result.WorkDir, path))
				if err != nil || string(got) != want {
					t.Fatalf("%s changed: %q %v", path, got, err)
				}
			}
			for _, path := range []string{execenv.TaskContextMarkerRelPath, filepath.Join(".omp", "mcp.json"), filepath.Join(".omp", "skills", "old-skill", "SKILL.md")} {
				if _, err := os.Stat(filepath.Join(result.WorkDir, path)); !os.IsNotExist(err) {
					t.Fatalf("legacy context remains in shared code: %s %v", path, err)
				}
			}
		})
	}
}

func TestFamilyLegacyRootAdoptionDoesNotChangeActiveRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	d, _, cleanup := newLeaderReuseTestDaemon(t)
	defer cleanup()
	root := leaderReuseTestTask("active-legacy-root")
	env, err := execenv.Prepare(execenv.PrepareParams{WorkspacesRoot: d.cfg.WorkspacesRoot, WorkspaceID: root.WorkspaceID, TaskID: root.ID, RuntimeID: root.RuntimeID, Provider: "claude", Task: execenv.TaskContextForEnv{IssueID: root.IssueID, AgentID: root.AgentID}}, d.logger)
	if err != nil {
		t.Fatal(err)
	}
	defer env.ReleaseLock()
	child := root
	child.CodeWorkspaceIssueID, child.CodeWorkspaceWorkDir = root.IssueID, env.WorkDir
	if err := d.adoptFamilyWorkspace(t.Context(), child, nil, ""); err == nil {
		t.Fatal("adopted an exclusively held legacy root")
	}
	env.ReleaseLock()
	lease, err := execenv.UseSharedDirectory(t.Context(), env.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Finish(t.Context(), nil)
	if err := d.adoptFamilyWorkspace(t.Context(), child, nil, ""); err == nil {
		t.Fatal("adopted a root with a live borrowed consumer")
	}
	if _, err := os.Stat(filepath.Join(env.WorkDir, execenv.TaskContextMarkerRelPath)); err != nil {
		t.Fatal("live root context was removed")
	}
	scope, err := execenv.ReadManagedEnvProvenance(env.RootDir)
	if err != nil || scope.CodeWorkspaceIssueID != "" {
		t.Fatal("live root ownership was changed")
	}
}
