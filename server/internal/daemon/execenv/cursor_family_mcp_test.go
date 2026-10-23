package execenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFamilyManagedCursorKeepsMcpCredentialsInPrivatePlugins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	params := PrepareParams{WorkspacesRoot: t.TempDir(), WorkspaceID: "ws", RuntimeID: "runtime", TaskID: turnOneTask, Provider: "cursor", IsolateLocalContext: true,
		Task:      TaskContextForEnv{IssueID: "child-one", AgentID: "agent-one", CodeWorkspaceIssueID: "root"},
		McpConfig: json.RawMessage(`{"mcpServers":{"example":{"url":"https://example.test/mcp","headers":{"Authorization":"first-run-only"}}}}`)}
	first, err := Prepare(params, worktreeTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer first.ReleaseLock()
	writeFile(t, filepath.Join(first.WorkDir, "shared.txt"), "shared code")
	claim, err := ClaimEnvRoot(RootDirParams{WorkspacesRoot: params.WorkspacesRoot, WorkspaceID: params.WorkspaceID, TaskID: turnTwoTask})
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Release()
	secondTask := params.Task
	secondTask.IssueID, secondTask.AgentID = "child-two", "agent-two"
	second := Reuse(ReuseParams{WorkspacesRoot: params.WorkspacesRoot, RunRoot: claim.RootDir(), WorkDir: first.WorkDir, Provider: "cursor", IsolateLocalContext: true,
		Task: secondTask, McpConfig: json.RawMessage(`{"mcpServers":{"example":{"url":"https://example.test/mcp","headers":{"Authorization":"second-run-only"}}}}`)}, worktreeTestLogger())
	if second == nil {
		t.Fatal("Cursor sibling could not start with shared code and managed MCP")
	}
	if first.CursorMcpPluginDir == "" || second.CursorMcpPluginDir == "" || first.CursorMcpPluginDir == second.CursorMcpPluginDir || first.CursorDataDir == second.CursorDataDir {
		t.Fatal("Cursor plugin/configuration state was shared")
	}
	if second.WorkDir != first.WorkDir || readFile(t, filepath.Join(second.WorkDir, "shared.txt")) != "shared code" {
		t.Fatal("Cursor siblings did not share code")
	}
	for _, run := range []struct {
		env    *Environment
		secret string
	}{{first, "first-run-only"}, {second, "second-run-only"}} {
		config := filepath.Join(run.env.CursorMcpPluginDir, "mcp.json")
		if raw := readFile(t, config); !strings.Contains(raw, run.secret) {
			t.Fatalf("managed MCP lost the run's credentials: %s", config)
		}
		if info, err := os.Stat(config); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("private MCP permissions: %v %v", info, err)
		}
		var manifest struct {
			Name       string `json:"name"`
			McpServers string `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(readFile(t, filepath.Join(run.env.CursorMcpPluginDir, ".cursor-plugin", "plugin.json"))), &manifest); err != nil || manifest.Name == "" || manifest.McpServers != "./mcp.json" {
			t.Fatalf("invalid Cursor plugin: %+v %v", manifest, err)
		}
	}
	if _, err := os.Stat(filepath.Join(first.WorkDir, ".cursor", "mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("managed MCP appeared in shared code: %v", err)
	}
	if err := CleanupSidecars(second.RootDir); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(first.CursorMcpPluginDir, "mcp.json")); !strings.Contains(got, "first-run-only") {
		t.Fatal("second run cleanup changed the first run's active MCP config")
	}
}

func TestFamilyCursorPrivateMcpKeepsRelativeProcessPaths(t *testing.T) {
	code := t.TempDir()
	_, plugin, err := prepareCursorPrivateMcpPlugin(t.TempDir(), code, json.RawMessage(`{"mcpServers":{"relative":{"command":"./server","cwd":"tools"},"path":{"command":"node","args":["tool.js"]}}}`), "", &sidecarManifest{})
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		McpServers map[string]struct {
			Command string `json:"command"`
			Cwd     string `json:"cwd"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(plugin, "mcp.json"))), &config); err != nil {
		t.Fatal(err)
	}
	if got := config.McpServers["relative"]; got.Cwd != filepath.Join(code, "tools") || got.Command != filepath.Join(code, "tools", "server") {
		t.Fatalf("private plugin changed relative command resolution: %+v", got)
	}
	if got := config.McpServers["path"]; got.Cwd != code || got.Command != "node" {
		t.Fatalf("private plugin changed PATH command resolution: %+v", got)
	}
}
