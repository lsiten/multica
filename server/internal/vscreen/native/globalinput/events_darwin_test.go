//go:build darwin && cgo

package globalinput

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Inspect real CoreGraphics events at the posting boundary without delivering
// keyboard or pointer input to the user's desktop.
func TestDarwinEventEncoding(t *testing.T) {
	source, err := os.ReadFile("globalinput_darwin.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(source), "static bool gi_trusted")
	end := strings.Index(string(source), "*/")
	if start < 0 || end <= start {
		t.Fatal("native event boundary missing")
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "events.inc"), source[start:end], 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	binary := filepath.Join(directory, "event-probe")
	args := []string{"clang", "-framework", "CoreGraphics", "-framework", "ApplicationServices", "-I", directory, "testdata/events.c", "-o", binary}
	if output, err := exec.CommandContext(ctx, "xcrun", args...).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v %s", err, output)
	}
	for _, scenario := range []string{"unicode", "drag", "wheel", "modifiers"} {
		t.Run(scenario, func(t *testing.T) {
			if output, err := exec.CommandContext(ctx, binary, scenario).CombinedOutput(); err != nil {
				t.Fatalf("event encoding: %v %s", err, output)
			}
		})
	}
}
