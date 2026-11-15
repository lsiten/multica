package agentdesktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func writeVersionFixture(t *testing.T, version string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is POSIX-only")
	}
	path := filepath.Join(t.TempDir(), "agent-desktop")
	body := `#!/bin/sh
case "$1" in
  version)
    printf '%s\n' '{"version":"2.6","ok":true,"command":"version","data":{"version":"` + version + `","os":"darwin","target":"aarch64"}}'
    ;;
  status)
    printf '%s\n' '{"version":"2.6","ok":true,"command":"status","data":{"platform":"macos","version":"` + version + `","permissions":{"accessibility":{"state":"granted"},"screen_recording":{"state":"granted"},"automation":{"state":"not_required"}},"supported_surfaces":["window"]}}'
    ;;
  permissions)
    printf '%s\n' '{"version":"2.6","ok":true,"command":"permissions","data":{"accessibility":{"state":"granted"},"screen_recording":{"state":"granted"},"automation":{"state":"not_required"}}}'
    ;;
  *)
    printf '%s\n' '{"version":"2.6","ok":false,"command":"unknown","error":{"code":"INVALID_ARGS"}}'
    exit 1
    ;;
esac
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyAcceptsPinnedVersion(t *testing.T) {
	path := writeVersionFixture(t, Version)
	if err := Verify(context.Background(), path); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsDifferentVersion(t *testing.T) {
	path := writeVersionFixture(t, "9.9.9")
	err := Verify(context.Background(), path)
	if err == nil {
		t.Fatal("version mismatch accepted")
	}
}

func TestProbeReadyRejectsAnEmptyAccessibilitySurface(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is POSIX-only")
	}
	path := filepath.Join(t.TempDir(), "agent-desktop")
	body := `#!/bin/sh
case "$1" in
  version)
    printf '%s\n' '{"version":"2.6","ok":true,"command":"version","data":{"version":"1.3.1","os":"darwin","target":"aarch64"}}'
    ;;
  status)
    printf '%s\n' '{"version":"2.6","ok":true,"command":"status","data":{"platform":"macos","version":"1.3.1","permissions":{"accessibility":{"state":"denied"}},"supported_surfaces":[]}}'
    ;;
esac
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Probe(context.Background(), path); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Probe error = %v, want ErrUnavailable", err)
	}
}

func TestInspectReportsDesktopReadiness(t *testing.T) {
	path := writeVersionFixture(t, Version)
	t.Setenv(ExecutableEnv, path)

	readiness, err := Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !readiness.Ready || readiness.Platform != "macos" || readiness.Version != Version ||
		readiness.Permissions.Accessibility != protocol.AgentDesktopPermissionGranted ||
		readiness.Permissions.ScreenRecording != protocol.AgentDesktopPermissionGranted ||
		readiness.SurfaceCount != 1 {
		t.Fatalf("unexpected readiness: %+v", readiness)
	}
}

func TestMCPConfigUsesBridgeAndTaskIdentity(t *testing.T) {
	raw, err := MCPConfig(BridgeConfig{
		MulticaExecutable: "/opt/multica",
		TaskID:            "task-1",
		RuntimeID:         "runtime-1",
		Executable:        "/opt/agent-desktop",
	})
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	entry := document.MCPServers[ServerName]
	if entry.Command != "/opt/multica" || len(entry.Args) == 0 || entry.Args[0] != BridgeCommand {
		t.Fatalf("unexpected config: %+v", document)
	}
}
