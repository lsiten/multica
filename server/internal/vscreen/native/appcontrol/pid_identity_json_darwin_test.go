//go:build darwin && cgo

package appcontrol

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Compile the actual identity function against synthetic process/signature readers;
// no real app, AX permission, event delivery or virtual display is used.
func TestPIDPartialIdentityMatchesActualGoJSON(t *testing.T) {
	source, err := os.ReadFile("identity_darwin.m")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(source), "NSDictionary *ACPIDProcess(pid_t pid)")
	end := strings.Index(string(source), "BOOL ACProcessEnded(")
	if start < 0 || end <= start {
		t.Fatal("native identity boundary missing")
	}
	directory := t.TempDir()
	if err = os.WriteFile(filepath.Join(directory, "pid-process-source.inc"), source[start:end], 0600); err != nil {
		t.Fatal(err)
	}
	base := Process{PID: 123, UID: 501, Start: "started", BundleID: "owned.fixture", OSBuild: "test-os"}
	partial := base
	partial.ExecutablePath = "/owned/fixture"
	partial.SigningID = "selected-helper"
	partial.CodeHash = strings.Repeat("a", 40)
	full := partial
	full.AppVersion = "1"
	full.AppBuild = "2"
	raw, err := json.Marshal(map[string]Process{"partial": partial, "full": full, "empty": base})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(directory, "go-process.json")
	if err = os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	binary := filepath.Join(directory, "identity-probe")
	args := []string{"clang", "-fobjc-arc", "-framework", "Foundation", "-I", directory, "testdata/pid-identity-json/probe.m", "-o", binary}
	if output, e := exec.CommandContext(ctx, "xcrun", args...).CombinedOutput(); e != nil {
		t.Fatalf("compile: %v %s", e, output)
	}
	output, err := exec.CommandContext(ctx, binary, input).CombinedOutput()
	t.Logf("native_source_sha256=%x go_json_sha256=%x observable=%s", sha256.Sum256(source), sha256.Sum256(raw), output)
	if err != nil {
		t.Fatalf("Go/native identity contract: %v", err)
	}
}
