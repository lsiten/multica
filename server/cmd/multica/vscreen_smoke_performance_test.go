package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPerformanceSmokeEntryRequiresOptInBeforeFilesystem(t *testing.T) {
	t.Setenv("MULTICA_RUN_VSCREEN_GUI_SMOKE", "")
	path := filepath.Join(t.TempDir(), "not-created")
	if err := runVscreenPerformanceSmoke(&bytes.Buffer{}, path); err == nil || err.Error() != "gui_not_authorized" {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("entry created resources before GUI authorization")
	}
}
func TestPerformanceSmokeEntryRequiresPrivateEvidenceDirectory(t *testing.T) {
	t.Setenv("MULTICA_RUN_VSCREEN_GUI_SMOKE", "1")
	path := t.TempDir()
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := runVscreenPerformanceSmoke(&bytes.Buffer{}, path); err == nil || err.Error() != "private_performance_directory_required" {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, "performance-config.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("entry wrote a nonce into a public directory")
	}
}
