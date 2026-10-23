//go:build unix

package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCursorExecutesWithPrivateMcpPluginAndSharedCode(t *testing.T) {
	shared, private := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(private, "mcp.json"), []byte(`{"mcpServers":{"fake":{"command":"test-only"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(t.TempDir(), "cursor-agent")
	writeTestExecutable(t, fake, []byte(`#!/bin/sh
set -eu
plugin=''
workspace=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    --plugin-dir) shift; plugin="$1" ;;
    --workspace) shift; workspace="$1" ;;
  esac
  shift
done
cat >/dev/null
test -f "$plugin/mcp.json"
test ! -e "$workspace/.cursor/mcp.json"
test "$(cd "$workspace" && pwd -P)" = "$(pwd -P)"
printf '%s' 'shared-code-edit' > "$workspace/result.txt"
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"connected","session_id":"private-mcp"}'
`))
	backend, err := New("cursor", Config{ExecutablePath: fake, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := backend.Execute(ctx, "test", ExecOptions{Cwd: shared, CursorMcpPluginDir: private})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Messages {
	}
	result := <-session.Result
	if result.Status != "completed" {
		t.Fatalf("private MCP provider startup failed: %+v", result)
	}
	if data, err := os.ReadFile(filepath.Join(shared, "result.txt")); err != nil || string(data) != "shared-code-edit" {
		t.Fatalf("provider did not edit shared code: %q %v", data, err)
	}
}
