package localreview

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestContextPageReadsExactLinesFromCapturedBytes(t *testing.T) {
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	blob, err := store.Put(t.Context(), strings.NewReader("one\n二\r\nthree\nlast"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.ContextPage(t.Context(), CachedPatchPage{Blob: blob, Page: PatchPageRequest{Offset: 1, Limit: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Lines) != 2 || page.Lines[0].Text != "二" || page.Lines[0].NewLine != 2 || page.Lines[1].Text != "three" || page.NextLine != 3 || !page.HasMore {
		t.Fatalf("wrong captured context: %+v", page)
	}
}

func TestPatchHunkContextGapSurvivesPagination(t *testing.T) {
	patch := "@@ -5,1 +5,1 @@\n same\n@@ -10,1 +10,1 @@\n another\n"
	page, err := ReadPatchPage(t.Context(), strings.NewReader(patch), PatchPageRequest{Offset: 2, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Lines) != 1 {
		t.Fatal("missing hunk header")
	}
	hunk := page.Lines[0]
	if hunk.ContextOldStart != 6 || hunk.ContextNewStart != 6 || hunk.ContextLines != 4 {
		t.Fatalf("wrong omitted context: %+v", hunk)
	}
}

func TestContextPageRejectsTamperingOutsideSelectedLines(t *testing.T) {
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	blob, err := store.Put(t.Context(), strings.NewReader("one\ntwo\n"))
	if err != nil {
		t.Fatal(err)
	}
	file, err := store.root.OpenFile(blob.ID, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteAt([]byte("bad"), 4)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	page, err := store.ContextPage(t.Context(), CachedPatchPage{Blob: blob, Page: PatchPageRequest{Limit: 1}})
	if !errors.Is(err, ErrSnapshotContentChanged) || len(page.Lines) != 0 {
		t.Fatal("unverified context escaped the snapshot boundary", err)
	}
}
