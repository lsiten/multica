package localreview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlobStorePagesLargeFrozenPatch(t *testing.T) {
	store, err := OpenBlobStore(t.TempDir(), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	patch := "@@ -0,0 +1,600000 @@\n" + strings.Repeat("+preserved content\n", 600000)
	blob, err := store.Put(t.Context(), strings.NewReader(patch))
	if err != nil {
		t.Fatal(err)
	}
	if blob.Size <= 8<<20 {
		t.Fatal("fixture must exceed old whole-review limit")
	}
	page, err := store.PatchPage(t.Context(), CachedPatchPage{Blob: blob, Page: PatchPageRequest{Offset: 1, Limit: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Lines) != 5 || page.Lines[0].Text != "+preserved content" || page.Lines[4].NewLine != 5 || !page.HasMore {
		t.Fatalf("unexpected frozen page: %+v", page)
	}
}

func TestBlobStoreRejectsTamperedContentBeforeReturningPage(t *testing.T) {
	root := t.TempDir()
	store, err := OpenBlobStore(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	blob, err := store.Put(t.Context(), strings.NewReader("+original\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".local-review-cache", "blobs", blob.ID), []byte("+replaced\n"), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := store.PatchPage(t.Context(), CachedPatchPage{Blob: blob, Page: PatchPageRequest{Limit: 5}})
	if !errors.Is(err, ErrSnapshotContentChanged) || len(page.Lines) != 0 {
		t.Fatalf("tampered content exposed: %+v, %v", page, err)
	}
}

func TestBlobStoreRejectsSymlinkCache(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, ".local-review-cache")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store, err := OpenBlobStore(root, 1<<20)
	if store != nil {
		store.Close()
	}
	if err == nil {
		t.Fatal("accepted redirected snapshot cache")
	}
}

func TestBlobStoreRejectsOversizedFileWithoutPublishingPartialBlob(t *testing.T) {
	root := t.TempDir()
	store, err := OpenBlobStore(root, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.Put(t.Context(), strings.NewReader(strings.Repeat("x", 17)))
	if !errors.Is(err, ErrSnapshotBlobTooLarge) {
		t.Fatalf("unexpected error: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".local-review-cache", "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("partial blob was published")
	}
}

func TestBlobStoreHonorsCancelledCapture(t *testing.T) {
	store, err := OpenBlobStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = store.Put(ctx, strings.NewReader("contents"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBlobStoreDetectsTamperingBeyondRequestedPage(t *testing.T) {
	root := t.TempDir()
	store, err := OpenBlobStore(root, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	contents := strings.Repeat("+original line\n", 50000)
	blob, err := store.Put(t.Context(), strings.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(root, ".local-review-cache", "blobs", blob.ID), os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteAt([]byte("X"), blob.Size-2)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		t.Fatal(err)
	}
	page, err := store.PatchPage(t.Context(), CachedPatchPage{Blob: blob, Page: PatchPageRequest{Limit: 1}})
	if !errors.Is(err, ErrSnapshotContentChanged) || len(page.Lines) != 0 {
		t.Fatalf("unverified page exposed: %+v %v", page, err)
	}
}

func TestBlobStoreRejectsArbitraryPathsAndSymlinkBlobs(t *testing.T) {
	root := t.TempDir()
	store, err := OpenBlobStore(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"../private", "/etc/passwd", strings.Repeat("A", 64), strings.Repeat("0", 63)} {
		_, err := store.PatchPage(t.Context(), CachedPatchPage{Blob: BlobRef{ID: id}, Page: PatchPageRequest{Limit: 1}})
		if !errors.Is(err, ErrInvalidSnapshotBlob) {
			t.Fatalf("accepted invalid reference %q: %v", id, err)
		}
	}
	id := strings.Repeat("0", 64)
	if err := os.Symlink(filepath.Join(root, "private"), filepath.Join(root, ".local-review-cache", "blobs", id)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err = store.PatchPage(t.Context(), CachedPatchPage{Blob: BlobRef{ID: id}, Page: PatchPageRequest{Limit: 1}})
	if !errors.Is(err, ErrSnapshotContentChanged) {
		t.Fatalf("accepted symlink blob: %v", err)
	}
}
