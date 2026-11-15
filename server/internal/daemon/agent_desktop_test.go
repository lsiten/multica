package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/multica-ai/multica/server/internal/agentdesktop"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func agentDesktopVersionFixture(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is POSIX-only")
	}
	path := filepath.Join(t.TempDir(), "agent-desktop")
	body := `#!/bin/sh
case "$1" in
  version)
    printf '%s\n' '{"ok":true,"data":{"version":"1.3.1","os":"darwin","target":"aarch64"}}'
    ;;
  status)
    printf '%s\n' '{"ok":true,"data":{"platform":"macos","version":"1.3.1","permissions":{"accessibility":{"state":"granted"},"screen_recording":{"state":"granted"},"automation":{"state":"not_required"}},"supported_surfaces":["window"]}}'
    ;;
  permissions)
    printf '%s\n' '{"ok":true,"data":{"accessibility":{"state":"granted"},"screen_recording":{"state":"granted"},"automation":{"state":"not_required"}}}'
    ;;
esac
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(agentdesktop.ExecutableEnv, path)
	return path
}

func TestStartTaskAgentDesktopInjectsOnlyAgentDesktop(t *testing.T) {
	agentDesktopVersionFixture(t)
	d := &Daemon{cfg: Config{NativeHostExecutable: "/opt/multica"}}
	task := Task{
		ID:           "task-1",
		RuntimeID:    "runtime-1",
		MirrorSource: &protocol.MirrorSourceBinding{},
	}
	raw, execution, err := d.startTaskAgentDesktop(t.Context(), task, "codex")
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	var document struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.MCPServers) != 1 || document.MCPServers[agentdesktop.ServerName] == nil {
		t.Fatalf("unexpected MCP servers: %s", raw)
	}
	if _, exists := document.MCPServers["multica-vscreen"]; exists {
		t.Fatal("legacy desktop server was injected")
	}
}

func TestAgentDesktopLeaseSerializesOneRuntime(t *testing.T) {
	agentDesktopVersionFixture(t)
	d := &Daemon{cfg: Config{NativeHostExecutable: "/opt/multica"}}
	firstTask := Task{ID: "task-1", RuntimeID: "runtime-1", MirrorSource: &protocol.MirrorSourceBinding{}}
	secondTask := Task{ID: "task-2", RuntimeID: "runtime-1", MirrorSource: &protocol.MirrorSourceBinding{}}
	_, first, err := d.startTaskAgentDesktop(t.Context(), firstTask, "codex")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, _, err = d.startTaskAgentDesktop(t.Context(), secondTask, "codex"); err == nil {
		t.Fatal("second desktop task acquired the same runtime")
	}
	first.Close()
	_, second, err := d.startTaskAgentDesktop(t.Context(), secondTask, "codex")
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
}

func TestAgentDesktopRejectsUnsupportedProvider(t *testing.T) {
	d := &Daemon{cfg: Config{NativeHostExecutable: "/opt/multica"}}
	task := Task{ID: "task-1", RuntimeID: "runtime-1", MirrorSource: &protocol.MirrorSourceBinding{}}
	if _, _, err := d.startTaskAgentDesktop(t.Context(), task, "unknown"); err == nil {
		t.Fatal("unsupported provider accepted")
	}
}
