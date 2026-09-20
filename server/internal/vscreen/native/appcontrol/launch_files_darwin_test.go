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

func TestNativeLaunchFilesValidation(t *testing.T) {
	source, err := os.ReadFile("workspace_darwin.m")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "static NSArray<NSURL *> *ACRequestedFiles")
	end := strings.Index(text, "NSDictionary *ACLaunch")
	if start < 0 || end <= start {
		t.Fatal("launch files helper boundary missing")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "input.txt")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "launch-files.inc"), []byte(text[start:end]), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	binary := filepath.Join(directory, "launch-files-probe")
	args := []string{"clang", "-fobjc-arc", "-framework", "Foundation", "-I", directory, "testdata/launch-files/probe.m", "-o", binary}
	if output, e := exec.CommandContext(ctx, "xcrun", args...).CombinedOutput(); e != nil {
		t.Fatalf("compile: %v %s", e, output)
	}
	if output, e := exec.CommandContext(ctx, binary, path, directory).CombinedOutput(); e != nil {
		t.Fatalf("native launch-files validation: %v %s", e, output)
	}
}
