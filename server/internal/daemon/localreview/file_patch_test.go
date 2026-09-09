package localreview

import (
	"strings"
	"testing"
)

func TestFilePatchUsesCapturedContentNotWorkingDirectory(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "captured change\n")
	run(t, repo, "commit", "-am", "capture")
	taskRoot := t.TempDir()
	store, err := OpenBlobStore(taskRoot, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureCommitted(t.Context(), CommittedRequest{Path: repo, Branch: "feature", Head: run(t, repo, "rev-parse", "HEAD"), Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	write(t, repo, "app.txt", "later edit must not leak\n")
	patch, err := store.FilePatch(t.Context(), version.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.PatchPage(t.Context(), CachedPatchPage{Blob: patch, Page: PatchPageRequest{Limit: 50}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range page.Lines {
		if strings.Contains(line.Text, "later edit") {
			t.Fatal("read live content instead of captured version")
		}
		if line.Text == "+captured change" {
			found = true
		}
	}
	if !found {
		t.Fatalf("captured diff missing: %+v", page)
	}
	// A second page request opens a fresh store and must not invoke Git again.
	reopened, err := OpenBlobStore(taskRoot, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	t.Setenv("PATH", t.TempDir())
	again, err := reopened.FilePatch(t.Context(), version.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	if patch != again {
		t.Fatal("temporary path changed patch identity")
	}
}
