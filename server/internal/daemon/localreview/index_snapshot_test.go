package localreview

import (
	"strings"
	"testing"
)

func TestStagedSnapshotDoesNotReadNewerWorkingBytes(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "staged version\n")
	run(t, repo, "add", "app.txt")
	write(t, repo, "app.txt", "working version\n")
	status, err := ReadIndexStatus(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureStaged(t.Context(), repo, status.IndexID)
	if err != nil {
		t.Fatal(err)
	}
	if len(version.Files) != 1 {
		t.Fatal("staged file missing")
	}
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
		if strings.Contains(line.Text, "working version") {
			t.Fatal("staged diff leaked newer working content")
		}
		if line.Text == "+staged version" {
			found = true
		}
	}
	if !found {
		t.Fatal("staged content missing")
	}
	after, err := ReadIndexStatus(t.Context(), repo)
	if err != nil || after.IndexID != status.IndexID {
		t.Fatal("inspection changed real index", err)
	}
}

func TestUnstagedSnapshotComparesWorkingBytesAgainstIndex(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "staged version\n")
	run(t, repo, "add", "app.txt")
	write(t, repo, "app.txt", "working version\n")
	status, err := ReadIndexStatus(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureUnstaged(t.Context(), repo, status.IndexID)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := store.FilePatch(t.Context(), version.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.PatchPage(t.Context(), CachedPatchPage{Blob: patch, Page: PatchPageRequest{Limit: 50}})
	if err != nil {
		t.Fatal(err)
	}
	old, current := false, false
	for _, line := range page.Lines {
		if line.Text == "-before" {
			t.Fatal("unstaged preview compared against HEAD instead of index")
		}
		old = old || line.Text == "-staged version"
		current = current || line.Text == "+working version"
	}
	if !old || !current {
		t.Fatal("missing index-to-working comparison")
	}
}
