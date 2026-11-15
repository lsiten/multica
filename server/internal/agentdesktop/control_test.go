package agentdesktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestControlSessionExecutesHeadedCommandsInTheStartedSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is POSIX-only")
	}
	path := filepath.Join(t.TempDir(), "agent-desktop")
	script := `#!/bin/sh
case "$1" in
  session)
    if [ "$2" = "start" ]; then
      printf '%s\n' '{"ok":true,"data":{"session_id":"session-1"}}'
    else
      printf '%s\n' '{"ok":true,"data":{}}'
    fi
    ;;
  *)
    printf '{"ok":true,"data":{"args":"%s"}}\n' "$*"
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	session, err := StartControlSession(context.Background(), path, "multica-test")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := session.Execute(context.Background(), []string{"mouse-click", "--xy", "10,20"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Args string `json:"args"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Args, "--session session-1") ||
		!strings.Contains(result.Args, "--headed") {
		t.Fatalf("control command omitted its lease: %q", result.Args)
	}
}
