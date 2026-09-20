//go:build darwin && cgo

package appcontrol

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeLaunchObserverStartupRetry(t *testing.T) {
	source, err := os.ReadFile("workspace_darwin.m")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "static AXError ACRegisterWindowObserver")
	end := strings.Index(text, "NSDictionary *ACLaunch")
	if start < 0 || end <= start {
		t.Fatal("launch observer helper boundary missing")
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "observer.inc"), []byte(text[start:end]), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	binary := filepath.Join(directory, "launch-observer-probe")
	args := []string{"clang", "-fobjc-arc", "-framework", "Foundation", "-framework", "ApplicationServices", "-I", directory, "testdata/launch-observer/probe.m", "-o", binary}
	if output, e := exec.CommandContext(ctx, "xcrun", args...).CombinedOutput(); e != nil {
		t.Fatalf("compile: %v %s", e, output)
	}
	if output, e := exec.CommandContext(ctx, binary).CombinedOutput(); e != nil {
		t.Fatalf("native launch observer retry: %v %s", e, output)
	}
}
