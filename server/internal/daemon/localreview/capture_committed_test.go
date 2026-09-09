package localreview

import (
	"strings"
	"testing"
)

func TestCaptureCommittedSupportsLargeDiffWithoutPatchPayload(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", strings.Repeat("a changed line\n", 700000))
	run(t, repo, "commit", "-am", "large change")
	head := run(t, repo, "rev-parse", "HEAD")
	write(t, repo, "app.txt", "unrelated dirty checkout\n")
	before := run(t, repo, "status", "--porcelain")
	store, err := OpenBlobStore(t.TempDir(), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureCommitted(t.Context(), CommittedRequest{Path: repo, Branch: "feature", Head: head, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(version.Files) != 1 || version.Files[0].New == nil || version.Files[0].New.Size <= 8<<20 || version.Files[0].Additions != 700000 || version.Header.Dirty || !version.Header.Committed {
		t.Fatalf("unexpected version: %+v", version)
	}
	if before != run(t, repo, "status", "--porcelain") {
		t.Fatal("capture changed checkout")
	}
	patch, err := store.FilePatch(t.Context(), version.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	if patch.Size <= 8<<20 {
		t.Fatal("expected a patch above the old response limit")
	}
	page, err := store.PatchPage(t.Context(), CachedPatchPage{Blob: patch, Page: PatchPageRequest{Limit: 30}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Lines) != 30 || !page.HasMore {
		t.Fatalf("large patch was not paged: %+v", page)
	}
	id, err := store.SaveVersion(t.Context(), version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadVersion(t.Context(), id); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureCommittedKeepsBinaryAndUnusualPaths(t *testing.T) {
	repo := repository(t)
	write(t, repo, "binary file.bin", "\x00\x01binary")
	write(t, repo, "name\twith\nlines.txt", "new content\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-m", "mixed files")
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureCommitted(t.Context(), CommittedRequest{Path: repo, Branch: "feature", Head: run(t, repo, "rev-parse", "HEAD"), Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(version.Files) != 2 {
		t.Fatalf("files: %+v", version.Files)
	}
	if version.Files[0].Path != "binary file.bin" || version.Files[0].Preview != "binary" || version.Files[1].Path != "name\twith\nlines.txt" {
		t.Fatalf("file identity lost: %+v", version.Files)
	}
}

func TestCaptureCommittedOversizedFileDoesNotHideOtherFiles(t *testing.T) {
	repo := repository(t)
	write(t, repo, "large.txt", strings.Repeat("x", 128))
	write(t, repo, "small.txt", "small\n")
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-m", "mixed sizes")
	store, err := OpenBlobStore(t.TempDir(), 64)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureCommitted(t.Context(), CommittedRequest{Path: repo, Branch: "feature", Head: run(t, repo, "rev-parse", "HEAD"), Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(version.Files) != 2 || version.Files[0].Preview != "too_large" || version.Files[0].NewOID == "" || version.Files[1].New == nil {
		t.Fatalf("file limit affected other files: %+v", version.Files)
	}
}
