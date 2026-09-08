package localreview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadStopsAtAggregateBudgetBeforeLaterFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires Unix")
	}
	path := repository(t)
	write(t, path, "a.txt", strings.Repeat("a", maxOutput/2+1))
	write(t, path, "b.txt", strings.Repeat("b", maxOutput/2))
	if err := os.Symlink(t.TempDir(), filepath.Join(path, "z-link")); err != nil {
		t.Fatal(err)
	}
	_, err := Read(context.Background(), path, "main")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("processed later files after exceeding budget: %v", err)
	}
}

func TestReadDoesNotExecuteConfiguredFSMonitor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires Unix")
	}
	path := repository(t)
	script := filepath.Join(t.TempDir(), "monitor")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho called > \"$0.called\"\nprintf '\\000'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	run(t, path, "config", "core.fsmonitor", script)
	if _, err := Read(context.Background(), path, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script + ".called"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("review invoked fsmonitor: %v", err)
	}
}

func TestReadDisablesFiltersAndShowsRawWorkingContent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires Unix")
	}
	for _, kind := range []string{"clean", "process"} {
		t.Run(kind, func(t *testing.T) {
			path := repository(t)
			write(t, path, ".gitattributes", "app.txt filter=reviewmarker\n")
			run(t, path, "add", ".gitattributes")
			run(t, path, "commit", "-m", "attributes")
			script := filepath.Join(t.TempDir(), "filter")
			if err := os.WriteFile(script, []byte("#!/bin/sh\necho called > \"$0.called\"\ncat\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			run(t, path, "config", "filter.reviewmarker."+kind, script)
			run(t, path, "config", "filter.reviewmarker.required", "true")
			write(t, path, "app.txt", "raw working content\n")
			snapshot, err := Read(context.Background(), path, "main")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(script + ".called"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("review executed configured filter", err)
			}
			found := false
			for _, file := range snapshot.Files {
				if file.Path == "app.txt" && strings.Contains(file.Patch, "+raw working content") {
					found = true
				}
			}
			if !found {
				t.Fatal("disabling filters hid the raw file diff")
			}
			if run(t, path, "config", "filter.reviewmarker."+kind) != script || run(t, path, "config", "filter.reviewmarker.required") != "true" {
				t.Fatal("review modified repository filter configuration")
			}
		})
	}
}
