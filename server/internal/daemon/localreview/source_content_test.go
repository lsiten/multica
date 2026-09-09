package localreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionContentReadsUncachedPinnedGitObject(t *testing.T) {
	repo := repository(t)
	original := strings.Repeat("reviewed\n", 100)
	write(t, repo, "large.txt", original)
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-m", "large")
	store, err := OpenBlobStore(t.TempDir(), 64)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureCommitted(t.Context(), CommittedRequest{Path: repo, Branch: "feature", Head: run(t, repo, "rev-parse", "HEAD"), Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	write(t, repo, "large.txt", "later contents")
	run(t, repo, "commit", "-am", "later")
	page, err := store.VersionContent(t.Context(), version, VersionContentRequest{FilePath: "large.txt", Side: "new", Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if page.Text != "reviewed" || page.Size != int64(len(original)) {
		t.Fatalf("did not read pinned content: %+v", page)
	}
}

func TestVersionContentVerifiesUncachedWorkingFingerprint(t *testing.T) {
	repo := repository(t)
	write(t, repo, "large.txt", strings.Repeat("original\n", 100))
	store, err := OpenBlobStore(t.TempDir(), 64)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.VersionContent(t.Context(), version, VersionContentRequest{FilePath: "large.txt", Side: "new", Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if page.Text != "original" {
		t.Fatalf("unexpected page: %+v", page)
	}
	write(t, repo, "large.txt", strings.Repeat("changed!\n", 100))
	page, err = store.VersionContent(t.Context(), version, VersionContentRequest{FilePath: "large.txt", Side: "new", Limit: 8})
	if err == nil || page.Text != "" {
		t.Fatal("changed live file was returned as captured content")
	}
}

func TestVersionContentIgnoresGitReplacementObjects(t *testing.T) {
	repo := repository(t)
	write(t, repo, "large.txt", strings.Repeat("original\n", 100))
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-m", "original")
	store, err := OpenBlobStore(t.TempDir(), 64)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureCommitted(t.Context(), CommittedRequest{Path: repo, Branch: "feature", Head: run(t, repo, "rev-parse", "HEAD"), Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	original := run(t, repo, "rev-parse", "HEAD:large.txt")
	write(t, repo, "replacement.txt", "unreviewed replacement")
	replacement := run(t, repo, "hash-object", "-w", "replacement.txt")
	run(t, repo, "replace", original, replacement)
	page, err := store.VersionContent(t.Context(), version, VersionContentRequest{FilePath: "large.txt", Side: "new", Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if page.Text != "original" {
		t.Fatalf("replacement ref altered captured content: %+v", page)
	}
}

func TestVersionContentDoesNotMaskCacheIntegrityFailure(t *testing.T) {
	repo := repository(t)
	write(t, repo, "app.txt", "original")
	run(t, repo, "commit", "-am", "original")
	cache := t.TempDir()
	store, err := OpenBlobStore(cache, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureCommitted(t.Context(), CommittedRequest{Path: repo, Branch: "feature", Head: run(t, repo, "rev-parse", "HEAD"), Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, ".local-review-cache", "blobs", version.Files[0].New.ID), []byte("replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := store.VersionContent(t.Context(), version, VersionContentRequest{FilePath: "app.txt", Side: "new", Limit: 8})
	if err == nil || page.Text != "" {
		t.Fatal("cache corruption was hidden by a fallback")
	}
}

func TestVersionContentUncachedSymlinkDoesNotReadItsTarget(t *testing.T) {
	repo := repository(t)
	private := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(private, []byte("must not be read"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, filepath.Join(repo, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store, err := OpenBlobStore(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	version, err := store.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.VersionContent(t.Context(), version, VersionContentRequest{FilePath: "link", Side: "new", Limit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if page.Text != private || page.Size != int64(len(private)) {
		t.Fatal("symlink target was read instead of link text")
	}
}
