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

func TestPointerRoutingPreservesWindowCoordinatesAndPrivateSource(t *testing.T) {
	source, err := os.ReadFile("input_darwin.m")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "static CGEventRef routeWindowPointer")
	end := strings.Index(text, "static NSString *post(")
	if start < 0 || end <= start {
		t.Fatal("window pointer routing helper missing")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "routing.inc"), []byte(text[start:end]), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	binary := filepath.Join(dir, "pointer-routing")
	if output, err := exec.CommandContext(ctx, "xcrun", "clang", "-fobjc-arc", "-framework", "AppKit", "-framework", "ApplicationServices", "-I", dir, "testdata/pointer-routing/probe.m", "-o", binary).CombinedOutput(); err != nil {
		t.Fatalf("compile routing probe: %v %s", err, output)
	}
	if output, err := exec.CommandContext(ctx, binary).CombinedOutput(); err != nil {
		t.Fatalf("pointer routing regression: %v %s", err, output)
	}
}
