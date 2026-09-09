package localreview

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in to avoid adding filesystem throughput measurements to normal unit runs.
// The fixture uses only a disposable Git repository, never installed agent CLIs.
func TestReviewScaleThousandFilesAndLargePatch(t *testing.T) {
	if os.Getenv("MULTICA_REVIEW_SCALE_TEST") != "1" {
		t.Skip("set MULTICA_REVIEW_SCALE_TEST=1 for the isolated scale fixture")
	}
	repo := repository(t)
	for index := 0; index < 1000; index++ {
		write(t, repo, fmt.Sprintf("file-%04d.txt", index), fmt.Sprintf("file %d\n", index)+strings.Repeat("bounded review fixture\n", 2048))
	}
	write(t, repo, "large.txt", strings.Repeat("large review fixture line\n", 500000))
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-m", "scale fixture")
	store, err := OpenBlobStore(t.TempDir(), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	started := time.Now()
	version, err := store.CaptureCommitted(t.Context(), CommittedRequest{Path: repo, Branch: "feature", Head: run(t, repo, "rev-parse", "HEAD"), Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	captureTime := time.Since(started)
	if len(version.Files) != 1001 {
		t.Fatalf("captured %d files", len(version.Files))
	}
	metadata, err := version.FilePage(FilePageRequest{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 256<<10 {
		t.Fatal("metadata response exceeded page bound")
	}
	var large VersionFile
	for _, file := range version.Files {
		if file.Path == "large.txt" {
			large = file
		}
	}
	started = time.Now()
	patch, err := store.FilePatch(t.Context(), large)
	if err != nil {
		t.Fatal(err)
	}
	coldTime := time.Since(started)
	if patch.Size <= 8<<20 {
		t.Fatal("fixture did not exceed the old aggregate limit")
	}
	started = time.Now()
	again, err := store.FilePatch(t.Context(), large)
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.PatchPage(t.Context(), CachedPatchPage{Blob: again, Page: PatchPageRequest{Offset: 250000, Limit: 200}})
	if err != nil {
		t.Fatal(err)
	}
	warmTime := time.Since(started)
	if len(page.Lines) != 200 || !page.HasMore {
		t.Fatal("deep patch page failed")
	}
	pageJSON, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(pageJSON) > 256<<10 {
		t.Fatal("patch response exceeded page bound")
	}
	workingStore, err := OpenBlobStore(t.TempDir(), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer workingStore.Close()
	write(t, repo, "file-0000.txt", "unstaged review fixture\n")
	started = time.Now()
	working, err := workingStore.CaptureWorking(t.Context(), WorkingVersionRequest{Path: repo, Target: "main"})
	if err != nil {
		t.Fatal(err)
	}
	workingTime := time.Since(started)
	if len(working.Files) != 1001 || !working.Header.Dirty {
		t.Fatal("working comparison omitted uncommitted changes")
	}
	t.Logf("files=%d capture=%s working_capture=%s cold_patch=%s warm_deep_page=%s patch_bytes=%d metadata_bytes=%d patch_page_bytes=%d", len(version.Files), captureTime, workingTime, coldTime, warmTime, patch.Size, len(encoded), len(pageJSON))
}
